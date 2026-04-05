package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Manifest parent command + init subcommand
// ---------------------------------------------------------------------------

var manifestCmd = &cobra.Command{
	Use:   "manifest",
	Short: "Manifest management commands",
}

var manifestInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Scan directory tree and generate manifest",
	RunE:  runManifestInit,
}

var (
	manifestInitDir    string
	manifestInitOutput string
	manifestInitDryRun bool
)

func init() {
	rootCmd.AddCommand(manifestCmd)
	manifestCmd.AddCommand(manifestInitCmd)

	manifestInitCmd.Flags().StringVar(&manifestInitDir, "dir", ".", "directory to scan")
	manifestInitCmd.Flags().StringVar(&manifestInitOutput, "output", "stdout", "output file path (stdout = print to stdout)")
	manifestInitCmd.Flags().BoolVar(&manifestInitDryRun, "dry-run", false, "show discovered repos without generating YAML")
}

// ---------------------------------------------------------------------------
// Data types used during scanning
// ---------------------------------------------------------------------------

// scannedRepo holds all information discovered about a single git repo.
type scannedRepo struct {
	absPath   string // absolute path of the repo on disk
	ghOwner   string // GitHub owner extracted from remote URL
	ghRepo    string // GitHub owner/name (e.g. "deligoez/tp")
	repoName  string // just the name part (e.g. "tp")
	ownerDir  string // absolute path of the inferred owner directory
	ownerFlat bool   // whether owner dir matched by name (false until layout is resolved)
	category  string // intermediate dir name, or empty for flat
}

// ownerLayout holds all repos for a single GitHub owner together with layout info.
type ownerLayout struct {
	ownerDirName string        // directory name used as the manifest owner key
	isFlat       bool          // all repos directly under owner dir
	repos        []scannedRepo // repos belonging to this owner
}

// ---------------------------------------------------------------------------
// GitHub remote URL parsing
// ---------------------------------------------------------------------------

var (
	reSSH   = regexp.MustCompile(`^git@github\.com:([^/]+)/([^/]+?)(?:\.git)?$`)
	reHTTPS = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+?)(?:\.git)?$`)
)

// parseGitHubRemote extracts (owner, repo-slug) from SSH or HTTPS GitHub URLs.
// Returns empty strings if the URL is not a recognised GitHub remote.
func parseGitHubRemote(url string) (owner, name string) {
	url = strings.TrimSpace(url)
	if m := reSSH.FindStringSubmatch(url); m != nil {
		return m[1], m[2]
	}
	if m := reHTTPS.FindStringSubmatch(url); m != nil {
		return m[1], m[2]
	}
	return "", ""
}

// ---------------------------------------------------------------------------
// Directory walk
// ---------------------------------------------------------------------------

// maxScanDepth is the maximum depth below --dir at which we look for .git dirs.
// depth 0 = --dir itself, max traversal = 4.
const maxScanDepth = 4

// walkForGitRepos walks the directory tree rooted at root up to maxScanDepth
// levels deep and returns each directory that contains a .git sub-directory.
// Symlinks and .git internal directories are skipped.
func walkForGitRepos(root string) ([]string, error) {
	var repos []string

	// filepath.WalkDir visits everything; we prune manually.
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable entries silently.
			return nil
		}

		// Skip symlinks entirely.
		if d.Type()&os.ModeSymlink != 0 {
			return filepath.SkipDir
		}

		if !d.IsDir() {
			return nil
		}

		// Determine depth relative to root.
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		depth := 0
		if rel != "." {
			depth = len(strings.Split(rel, string(os.PathSeparator)))
		}

		// Prune .git internal dirs (we handle .git presence below, not descent into it).
		if d.Name() == ".git" {
			return filepath.SkipDir
		}

		// Prune directories that are too deep to contain repos within maxScanDepth.
		// A repo at this path would need a .git child, so we need depth <= maxScanDepth.
		if depth > maxScanDepth {
			return filepath.SkipDir
		}

		// Check whether this directory is a git repo.
		gitDir := filepath.Join(path, ".git")
		if info, statErr := os.Lstat(gitDir); statErr == nil && info.IsDir() {
			repos = append(repos, path)
			// Don't descend into a repo — nested git repos are unusual enough that
			// we skip them (the parent is already recorded).
			return filepath.SkipDir
		}

		return nil
	})

	return repos, err
}

// ---------------------------------------------------------------------------
// Remote URL reading
// ---------------------------------------------------------------------------

// readOriginURL shells out to git to get the origin remote URL.
// Returns ("", nil) when no origin is configured.
func readOriginURL(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		// Exit code 2 means no such remote — not an error we need to propagate.
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

// ---------------------------------------------------------------------------
// Owner dir inference
// ---------------------------------------------------------------------------

// inferOwnerDir walks up from repoPath looking for an ancestor whose
// base-name matches ghOwner (case-insensitive). rootDir is the --dir
// boundary; we don't walk above it.
// Returns (ownerDirPath, foundByName). When no ancestor matches the owner
// name, the immediate parent is returned and foundByName is false.
func inferOwnerDir(repoPath, rootDir, ghOwner string) (string, bool) {
	parent := filepath.Dir(repoPath)
	lowerOwner := strings.ToLower(ghOwner)

	// Collect ancestors between repoPath (exclusive) and rootDir (inclusive).
	var ancestors []string
	cur := parent
	for {
		ancestors = append(ancestors, cur)
		if cur == rootDir {
			break
		}
		up := filepath.Dir(cur)
		if up == cur {
			// Reached filesystem root without hitting rootDir.
			break
		}
		cur = up
	}

	// Search from nearest to farthest.
	for _, anc := range ancestors {
		if strings.ToLower(filepath.Base(anc)) == lowerOwner {
			return anc, true
		}
	}

	// No match — use immediate parent as owner dir.
	return parent, false
}

// ---------------------------------------------------------------------------
// Layout resolution
// ---------------------------------------------------------------------------

// resolveLayouts groups scanned repos by their inferred owner directory and
// determines flat vs categorized layout per owner.
func resolveLayouts(repos []scannedRepo) []ownerLayout {
	// Group repos by ownerDir absolute path.
	type group struct {
		ownerDirName string
		foundByName  bool
		repos        []scannedRepo
	}
	byOwnerDir := make(map[string]*group)
	var ownerDirOrder []string

	for _, r := range repos {
		g, exists := byOwnerDir[r.ownerDir]
		if !exists {
			g = &group{
				ownerDirName: filepath.Base(r.ownerDir),
				foundByName:  !r.ownerFlat, // ownerFlat==false means we found by name
			}
			byOwnerDir[r.ownerDir] = g
			ownerDirOrder = append(ownerDirOrder, r.ownerDir)
		}
		g.repos = append(g.repos, r)
	}

	// Sort order by ownerDir path for deterministic output.
	sort.Strings(ownerDirOrder)

	var layouts []ownerLayout

	for _, odPath := range ownerDirOrder {
		g := byOwnerDir[odPath]

		// Determine depth of each repo relative to ownerDir.
		// depth 1 → flat (repo directly under ownerDir)
		// depth 2 → categorized (intermediate subdir is category)
		var depth1, depth2 []scannedRepo

		for _, r := range g.repos {
			rel, err := filepath.Rel(odPath, r.absPath)
			if err != nil {
				// Shouldn't happen; treat as depth-1.
				depth1 = append(depth1, r)
				continue
			}
			parts := strings.Split(rel, string(os.PathSeparator))
			switch len(parts) {
			case 1:
				depth1 = append(depth1, r)
			case 2:
				r.category = parts[0]
				depth2 = append(depth2, r)
			default:
				// Deeper than 2 — treat as depth-2 with nested category path.
				r.category = strings.Join(parts[:len(parts)-1], "/")
				depth2 = append(depth2, r)
			}
		}

		isFlat := len(depth1) > 0 && len(depth2) == 0
		isCategorized := len(depth2) > 0 && len(depth1) == 0

		var resolvedRepos []scannedRepo

		if isFlat {
			// All repos one level below ownerDir — flat.
			for i := range depth1 {
				depth1[i].category = ""
			}
			resolvedRepos = depth1
		} else if isCategorized {
			// All repos two levels below ownerDir — categorized.
			resolvedRepos = depth2
		} else {
			// Mixed — categorized, depth-1 repos go into "repos" category.
			if len(depth1) > 0 {
				fmt.Fprintf(os.Stderr, "warning: owner %q has mixed-depth repos; depth-1 repos placed in \"repos\" category\n", g.ownerDirName)
				for i := range depth1 {
					depth1[i].category = "repos"
				}
				resolvedRepos = append(depth2, depth1...)
			} else {
				resolvedRepos = depth2
			}
			isFlat = false
		}

		layouts = append(layouts, ownerLayout{
			ownerDirName: g.ownerDirName,
			isFlat:       isFlat,
			repos:        resolvedRepos,
		})
	}

	return layouts
}

// ---------------------------------------------------------------------------
// YAML generation
// ---------------------------------------------------------------------------

// generateYAML builds a YAML manifest from the discovered layouts.
// base_dir is derived from the scan root.
func generateYAML(scanRoot string, layouts []ownerLayout) ([]byte, error) {
	// Build yaml.Node tree manually to control formatting and key order.
	root := &yaml.Node{Kind: yaml.DocumentNode}
	mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	root.Content = append(root.Content, mapping)

	// base_dir
	addStringPair(mapping, "base_dir", scanRoot)

	// owners
	ownersKey := scalarNode("owners")
	ownersVal := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapping.Content = append(mapping.Content, ownersKey, ownersVal)

	for _, layout := range layouts {
		ownerKey := scalarNode(layout.ownerDirName)
		ownerVal := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}

		if layout.isFlat {
			addBoolPair(ownerVal, "flat", true)
		}

		// Group repos by category (preserving insertion order).
		type catEntry struct {
			name  string
			repos []scannedRepo
		}
		var catOrder []string
		catMap := make(map[string][]scannedRepo)

		for _, r := range layout.repos {
			cat := r.category
			if cat == "" {
				cat = "_flat_"
			}
			if _, ok := catMap[cat]; !ok {
				catOrder = append(catOrder, cat)
			}
			catMap[cat] = append(catMap[cat], r)
		}

		for _, cat := range catOrder {
			repos := catMap[cat]

			var catName string
			if cat == "_flat_" {
				// Flat repos are listed directly under the owner node without a category key.
				// According to the manifest spec, flat owners just have repo entries inline;
				// but the schema requires at least one category. We use "repos" as a fallback
				// category name for flat mode (the layout converter already handled this).
				// In practice _flat_ only appears when isFlat=true; produce a bare sequence.
				catName = "repos"
			} else {
				catName = cat
			}

			catKey := scalarNode(catName)
			catVal := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}

			for _, r := range repos {
				repoEntry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				addStringPair(repoEntry, "repo", r.ghRepo)
				catVal.Content = append(catVal.Content, repoEntry)
			}

			ownerVal.Content = append(ownerVal.Content, catKey, catVal)
		}

		ownersVal.Content = append(ownersVal.Content, ownerKey, ownerVal)
	}

	return yaml.Marshal(root)
}

// scalarNode creates a YAML scalar node with the given value.
func scalarNode(val string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: val}
}

// addStringPair appends a key/value string pair to a mapping node.
func addStringPair(m *yaml.Node, key, val string) {
	m.Content = append(m.Content, scalarNode(key), scalarNode(val))
}

// addBoolPair appends a key/value bool pair to a mapping node.
func addBoolPair(m *yaml.Node, key string, val bool) {
	valStr := "false"
	if val {
		valStr = "true"
	}
	m.Content = append(m.Content, scalarNode(key), &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!bool",
		Value: valStr,
	})
}

// ---------------------------------------------------------------------------
// Main command handler
// ---------------------------------------------------------------------------

// tildeCollapse replaces the home directory prefix with ~.
func tildeCollapse(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

func runManifestInit(cmd *cobra.Command, args []string) error {
	// 1. Resolve --dir to an absolute path.
	scanRoot, err := filepath.Abs(manifestInitDir)
	if err != nil {
		return fmt.Errorf("resolving --dir: %w", err)
	}

	info, err := os.Stat(scanRoot)
	if err != nil {
		return fmt.Errorf("cannot access --dir %q: %w", scanRoot, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("--dir %q is not a directory", scanRoot)
	}

	// 2. Check --output before doing any work.
	writeToFile := manifestInitOutput != "" && manifestInitOutput != "stdout"
	if writeToFile && !manifestInitDryRun {
		if _, statErr := os.Stat(manifestInitOutput); statErr == nil {
			return fmt.Errorf("output file %q already exists; delete it first", manifestInitOutput)
		}
	}

	// 3. Walk directory tree.
	repoPaths, walkErr := walkForGitRepos(scanRoot)
	if walkErr != nil {
		return fmt.Errorf("walking directory: %w", walkErr)
	}

	// 4. Read remote URLs and parse GitHub info.
	var discovered []scannedRepo
	var skipped int

	for _, repoPath := range repoPaths {
		originURL, urlErr := readOriginURL(repoPath)
		if urlErr != nil {
			fmt.Fprintf(os.Stderr, "skipping %s: could not read remote: %v\n", repoPath, urlErr)
			skipped++
			continue
		}
		if originURL == "" {
			fmt.Fprintf(os.Stderr, "skipping %s: no origin remote\n", repoPath)
			skipped++
			continue
		}

		ghOwner, ghName := parseGitHubRemote(originURL)
		if ghOwner == "" {
			fmt.Fprintf(os.Stderr, "skipping %s: non-GitHub or unparseable remote %q\n", repoPath, originURL)
			skipped++
			continue
		}

		ownerDir, foundByName := inferOwnerDir(repoPath, scanRoot, ghOwner)

		repo := scannedRepo{
			absPath:   repoPath,
			ghOwner:   ghOwner,
			ghRepo:    ghOwner + "/" + ghName,
			repoName:  ghName,
			ownerDir:  ownerDir,
			ownerFlat: !foundByName, // ownerFlat==true means we fell back to immediate parent
		}
		discovered = append(discovered, repo)
	}

	// 5. Dry-run output.
	if manifestInitDryRun {
		fmt.Printf("Found %d repos:\n", len(discovered))
		for _, r := range discovered {
			label := fmt.Sprintf("%s/%s", r.ghOwner, r.repoName)
			fmt.Printf("  %-30s %s\n", label, tildeCollapse(r.absPath))
		}
		if skipped > 0 {
			fmt.Printf("  (%d repos skipped — no GitHub remote)\n", skipped)
		}
		return nil
	}

	if len(discovered) == 0 {
		return fmt.Errorf("no GitHub repos found under %q", scanRoot)
	}

	// 6. Resolve layouts (flat vs categorized per owner).
	layouts := resolveLayouts(discovered)

	// 7. Generate YAML.
	yamlBytes, genErr := generateYAML(scanRoot, layouts)
	if genErr != nil {
		return fmt.Errorf("generating YAML: %w", genErr)
	}

	// 8. Write output.
	if writeToFile {
		if writeErr := os.WriteFile(manifestInitOutput, yamlBytes, 0644); writeErr != nil {
			return fmt.Errorf("writing output file: %w", writeErr)
		}
		fmt.Fprintf(os.Stderr, "manifest written to %s\n", manifestInitOutput)
	} else {
		fmt.Print(string(yamlBytes))
	}

	return nil
}
