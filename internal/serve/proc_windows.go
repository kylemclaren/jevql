//go:build windows

package serve

import "os"

func processAlive(pid int) bool {
	_, err := os.FindProcess(pid)
	return err == nil
}
