//go:build !windows

package service

// systemTool returns name unchanged: only Windows resolves its tools by
// absolute system path (review 55 R55-090).
func systemTool(name string) (string, error) { return name, nil }
