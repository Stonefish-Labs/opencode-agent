package process

import "os/exec"

type Controller interface {
	Configure(*exec.Cmd)
	Terminate(pid int)
	Kill(pid int)
	Alive(pid int) bool
}

var Default Controller = defaultController{}
