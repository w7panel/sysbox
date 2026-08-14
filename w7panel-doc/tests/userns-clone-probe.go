//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func probe(name, path string, args []string, hostID int, ptrace, pidfd, unshareNet bool) {
	attr := &syscall.SysProcAttr{
		Cloneflags:                 syscall.CLONE_NEWUSER,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: hostID, Size: 65536}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: hostID, Size: 65536}},
		GidMappingsEnableSetgroups: true,
		Ptrace:                     ptrace,
		Pdeathsig:                  syscall.SIGKILL,
	}
	if unshareNet {
		attr.Unshareflags = syscall.CLONE_NEWNET
	}
	var fd int
	if pidfd {
		attr.PidFD = &fd
	}
	proc, err := os.StartProcess(path, args, &os.ProcAttr{
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
		Sys:   attr,
	})
	if err != nil {
		fmt.Printf("%s: ERROR: %v\n", name, err)
		return
	}
	fmt.Printf("%s: STARTED pid=%d pidfd=%d\n", name, proc.Pid, fd)
	_ = proc.Kill()
	_, _ = proc.Wait()
}

func main() {
	if filepath.Base(os.Args[0]) == "probe-child" {
		select {}
	}
	probe("plain", "/bin/true", []string{"true"}, 0, false, false, false)
	probe("pidfd", "/bin/true", []string{"true"}, 0, false, true, false)
	probe("ptrace", "/bin/true", []string{"true"}, 0, true, false, false)
	probe("pidfd+ptrace", "/bin/true", []string{"true"}, 0, true, true, false)
	probe("containerd-identity", "/proc/self/exe", []string{"probe-child"}, 0, true, true, true)
	probe("containerd-kubelet-map", "/proc/self/exe", []string{"probe-child"}, 2227961856, true, true, true)
}
