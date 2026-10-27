package main

import (
	"os"
	"testing"
)

func TestMainExecution(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	os.Args = []string{"wings", "version"}
	main()
}
