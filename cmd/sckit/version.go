package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/LocalKinAI/sckit-go"
)

func runVersion(args []string) int {
	// Lazy-load the dylib so ResolvedDylibPath is populated.
	_ = sckit.Load()

	fmt.Printf("sckit v%s\n", sckit.Version)
	fmt.Printf("  go:     %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Printf("  macOS:  %s\n", macOSVersion())
	if p := sckit.ResolvedDylibPath(); p != "" {
		if info, err := os.Stat(p); err == nil {
			fmt.Printf("  dylib:  %s (%d bytes)\n", p, info.Size())
		} else {
			fmt.Printf("  dylib:  %s\n", p)
		}
	} else {
		fmt.Println("  dylib:  (not yet loaded)")
	}
	return 0
}

func macOSVersion() string {
	out, err := exec.Command("sw_vers", "-productVersion").Output()
	if err != nil {
		return "(unknown — sw_vers failed)"
	}
	v := strings.TrimSpace(string(out))
	out, err = exec.Command("sw_vers", "-productName").Output()
	if err != nil {
		return v
	}
	name := strings.TrimSpace(string(out))
	return fmt.Sprintf("%s %s", name, v)
}
