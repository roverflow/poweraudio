package tui

import "syscall"

func syscallProcAttr() syscall.SysProcAttr {
	return syscall.SysProcAttr{
		Setsid: true,
	}
}
