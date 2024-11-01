package internal

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed embed
var embedFS embed.FS

// Retrieves the application version from the embedded file system
// Returns an error if the file does not exist.
func GetVersion() string {
	vb, err := embedFS.ReadFile("embed/version.txt")
	if err != nil {
		return "0.0.0+undefined"
	}
	return strings.TrimSpace(string(vb))
}

// Retrieves a sub filesystem holding static content for the operator server.
// Returns an error if the folder does not exist.
func GetStaticFS() (fs.FS, error) {
	return fs.Sub(embedFS, "embed/static")
}
