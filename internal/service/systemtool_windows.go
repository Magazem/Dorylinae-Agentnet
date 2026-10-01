//go:build windows

package service

import "golang.org/x/sys/windows"

// systemTool resolves a bare Windows system tool name (schtasks.exe) to its
// absolute path under GetSystemDirectory, so a PATH entry or the working
// directory cannot substitute another binary (review 55 R55-090). Any other
// name, and any name when the system directory is unknown, is returned as is.
func systemTool(name string) string {
	if name != "schtasks.exe" {
		return name
	}
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		return name
	}
	return dir + `\` + name
}
