//go:build windows

package service

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// systemTool resolves a bare Windows system tool name (schtasks.exe) to its
// absolute path under GetSystemDirectory, so a PATH entry or the working
// directory cannot substitute another binary (review 55 R55-090). Any other
// name is returned as is. When the system directory is unknown it fails
// closed with an error rather than falling back to a PATH lookup.
func systemTool(name string) (string, error) {
	if name != "schtasks.exe" {
		return name, nil
	}
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		return "", fmt.Errorf("locate the Windows system directory for %s: %w", name, err)
	}
	return dir + `\` + name, nil
}
