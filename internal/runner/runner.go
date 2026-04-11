package runner

import (
	"bytes"
	"fmt"
	"os/exec"
)

func RunCommands(path string, commands []string) error {
	if len(commands) == 0 {
		return nil
	}

	for _, command := range commands {
		cmd := exec.Command("sh", "-c", command)
		cmd.Dir = path

		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("command %q failed: %w\n%s", command, err, stderr.String())
		}
	}

	return nil
}
