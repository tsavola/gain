package main

import (
	"flag"
	"log"
	"log/syslog"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"syscall"

	"github.com/tsavola/gate/internal/cred"
	"github.com/tsavola/gate/run"
)

func main() {
	var (
		config = run.Config{
			DaemonSocket: "/run/gate-containerd/socket",
			LibDir:       "lib",
			CgroupTitle:  run.DefaultCgroupTitle,
		}
		syslogging = false
	)

	flag.StringVar(&config.DaemonSocket, "socket", config.DaemonSocket, "listening unix socket")
	flag.UintVar(&config.CommonGid, "common-gid", config.CommonGid, "group id for file descriptor sharing")
	flag.UintVar(&config.ContainerCred.Uid, "container-uid", config.ContainerCred.Uid, "user id for bootstrapping executor")
	flag.UintVar(&config.ContainerCred.Gid, "container-gid", config.ContainerCred.Gid, "group id for bootstrapping executor")
	flag.UintVar(&config.ExecutorCred.Uid, "executor-uid", config.ExecutorCred.Uid, "user id for executing code")
	flag.UintVar(&config.ExecutorCred.Gid, "executor-gid", config.ExecutorCred.Gid, "group id for executing code")
	flag.StringVar(&config.LibDir, "libdir", config.LibDir, "path")
	flag.StringVar(&config.CgroupParent, "cgroup-parent", config.CgroupParent, "slice")
	flag.StringVar(&config.CgroupTitle, "cgroup-title", config.CgroupTitle, "prefix of dynamic name")
	flag.BoolVar(&syslogging, "syslog", syslogging, "send log messages to syslog instead of stderr")

	flag.Parse()

	var (
		critLog *log.Logger
		errLog  *log.Logger
		infoLog *log.Logger
	)

	if syslogging {
		tag := path.Base(os.Args[0])

		w, err := syslog.New(syslog.LOG_CRIT, tag)
		if err != nil {
			log.Fatal(err)
		}
		critLog = log.New(w, "", 0)

		w, err = syslog.New(syslog.LOG_ERR, tag)
		if err != nil {
			critLog.Fatal(err)
		}
		errLog = log.New(w, "", 0)

		w, err = syslog.New(syslog.LOG_INFO, tag)
		if err != nil {
			critLog.Fatal(err)
		}
		infoLog = log.New(w, "", 0)
	} else {
		critLog = log.New(os.Stderr, "", 0)
		errLog = critLog
		infoLog = critLog
	}

	if err := cred.ValidateIds("user", -1, 2, config.ContainerCred.Uid, config.ExecutorCred.Uid); err != nil {
		critLog.Fatal(err)
	}

	if err := cred.ValidateIds("group", -1, 2, config.ContainerCred.Gid, config.ExecutorCred.Gid, config.CommonGid); err != nil {
		critLog.Fatal(err)
	}

	if err := cred.ValidateId("group", config.CommonGid); err != nil {
		critLog.Fatal(err)
	}

	containerPath, err := filepath.Abs(path.Join(config.LibDir, "container"))
	if err != nil {
		return
	}

	containerArgs := []string{
		containerPath,
		cred.FormatId(config.CommonGid),
		cred.FormatId(config.ContainerCred.Uid),
		cred.FormatId(config.ContainerCred.Gid),
		cred.FormatId(config.ExecutorCred.Uid),
		cred.FormatId(config.ExecutorCred.Gid),
		config.CgroupTitle,
		config.CgroupParent,
	}

	addr, err := net.ResolveUnixAddr("unix", config.DaemonSocket)
	if err != nil {
		critLog.Fatal(err)
	}

	if info, err := os.Lstat(addr.Name); err == nil {
		if info.Mode()&os.ModeSocket != 0 {
			os.Remove(addr.Name)
		}
	}

	listener, err := net.ListenUnix(addr.Net, addr)
	if err != nil {
		critLog.Fatal(err)
	}
	defer listener.Close()

	for client := uint64(0); ; client++ {
		conn, err := listener.AcceptUnix()
		if err == nil {
			go handle(client, conn, containerArgs, errLog, infoLog)
		} else {
			errLog.Print(err)
		}
	}
}

func handle(client uint64, conn *net.UnixConn, containerArgs []string, errLog, infoLog *log.Logger) {
	infoLog.Printf("%d: connection", client)

	file, err := conn.File()
	conn.Close()
	if err != nil {
		errLog.Printf("%d: %v", client, err)
		return
	}

	cmd := exec.Cmd{
		Path:   containerArgs[0],
		Args:   containerArgs,
		Dir:    "/",
		Stderr: os.Stderr,
		ExtraFiles: []*os.File{
			file,
		},
		SysProcAttr: &syscall.SysProcAttr{
			Pdeathsig: syscall.SIGKILL,
		},
	}

	err = cmd.Start()
	file.Close()
	if err != nil {
		errLog.Printf("%d: %v", client, err)
		return
	}

	err = cmd.Wait()
	if exit, ok := err.(*exec.ExitError); ok && exit.Success() {
		infoLog.Printf("%d: %v", client, exit)
	} else {
		errLog.Printf("%d: %v", client, err)
	}
}
