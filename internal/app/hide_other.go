//go:build !windows

package app

import "os/exec"

func setHideWindow(*exec.Cmd) {}
