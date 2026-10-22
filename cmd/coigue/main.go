// Command coigue is a small terminal modal editor.
package main

import (
	"os"

	"github.com/fclaude/coigue/internal/app"
)

func main() {
	os.Exit(app.Main(os.Args[1:], os.Stdout, os.Stderr))
}
