package deps

import (
	"fmt"
	"os/exec"
)

func RunDeps(path string, commands []string) error {
	if len(commands) == 0 {
		return nil
	}

	for _, command := range commands {
		cmd := exec.Command("sh", "-c", command)
		cmd.Dir = path

		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("command %q failed: %w\nstderr: %s", command, err, output)
		}
	}

	return nil
}
