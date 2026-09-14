//go:build !linux

package install

import (
	"fmt"
	"os"
)

func checkSingleLink(os.FileInfo) error     { return fmt.Errorf("Linux installer only") }
func checkTrustedTool(os.FileInfo) error    { return fmt.Errorf("Linux installer only") }
func checkRootAncestors(string, bool) error { return fmt.Errorf("Linux installer only") }
func lockNativeInstall() (func(), error)    { return nil, fmt.Errorf("Linux installer only") }
