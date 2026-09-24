// Every Go file starts with a package clause. A directory is one package.
// The special name "main" tells the compiler this package builds into an
// executable, and that execution starts at the function named main.
//
// By convention, executables live under cmd/<binary-name>/. The binary
// produced from this directory is therefore called "hue".
package main

import (
	"fmt"
	"os"
)

// version is a plain package-level variable. The Makefile overwrites it at
// build time with the linker flag -X, so a released binary reports its real
// version while a local "go run" reports "dev".
var version = "dev"

// main is the entry point. It takes no arguments and returns nothing.
// Command-line arguments are read from os.Args, and the exit code is set by
// calling os.Exit. Keeping main tiny is a Go habit: it should only wire things
// together and translate a returned error into an exit code, so that the real
// logic lives in functions that can be unit tested.
func main() {
	if err := run(os.Args[1:]); err != nil {
		// fmt.Fprintln writes to any destination that implements io.Writer.
		// Errors go to stderr so that stdout stays clean for real output
		// (which matters once we add --json for scripting).
		fmt.Fprintln(os.Stderr, "hue:", err)
		os.Exit(1)
	}
}

// run holds the program logic. Returning an error instead of exiting directly
// is the idiomatic Go way to report failure: the caller decides what to do.
// Go has no exceptions. A function that can fail returns an error as its last
// result, and the caller checks it with "if err != nil".
//
// For now this is a placeholder that prints the version. The real command
// tree (discover, auth, lights ...) arrives in the next step.
func run(args []string) error {
	if len(args) == 0 {
		fmt.Println("hue", version)
		fmt.Println("usage: hue <command>   (no commands implemented yet)")
		return nil
	}
	// %q prints the string quoted, which makes empty or odd input visible.
	return fmt.Errorf("unknown command %q", args[0])
}
