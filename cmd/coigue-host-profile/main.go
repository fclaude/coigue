// Command coigue-host-profile generates the embedded Brote host profile.
package main

import (
	"encoding/json"
	"fmt"
	"github.com/fclaude/coigue/internal/ext"
	"os"
)

func main() {
	data, err := json.MarshalIndent(ext.HostProfile(), "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	data = append(data, '\n')
	if len(os.Args) == 2 {
		err = os.WriteFile(os.Args[1], data, 0644)
	} else {
		_, err = os.Stdout.Write(data)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
