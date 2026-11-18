package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"syscall"

	"github.com/gabriwar/anyscale"
	"github.com/gabriwar/anyscale/internal/locale"
	"github.com/gabriwar/anyscale/internal/mix"
	"github.com/gabriwar/anyscale/internal/ui"
)

func profile() func() {
	path, ok := os.LookupEnv("PPROF")
	if !ok {
		return func() {}
	}

	slog.Info("profiling enabled", "path", path)

	file, err := os.Create(path)
	if err != nil {
		panic(err)
	}

	err = pprof.StartCPUProfile(file)
	if err != nil {
		panic(err)
	}

	return func() {
		pprof.StopCPUProfile()
		err := file.Close()
		if err != nil {
			panic(err)
		}

		slog.Info("profiling stopped", "path", path)
	}
}

// runHelper runs a privileged anyscale operation as root. The GUI starts
// it with pkexec.
func runHelper(args []string) {
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "anyscale helper must run as root, through pkexec")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	err := mix.RunHelper(ctx, "", args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ctlCommands are anyscalectl's commands. Given one of them, anyscale
// runs anyscalectl instead of the app, so that "anyscale status" works
// rather than opening a file called status.
var ctlCommands = map[string]bool{
	"status": true, "up": true, "down": true, "update": true,
	"profiles": true, "routes": true, "exit-node": true, "dns": true,
	"wireguard": true, "tailscale": true, "ts": true, "completion": true,
	"version": true, "--socket-dir": true,
}

// runCtl replaces this process with anyscalectl, run with args. It
// looks next to this binary first, then in PATH.
func runCtl(args []string) {
	path := "anyscalectl"
	if exe, err := os.Executable(); err == nil {
		if p := filepath.Join(filepath.Dir(exe), "anyscalectl"); fileExists(p) {
			path = p
		}
	}
	if !filepath.IsAbs(path) {
		p, err := exec.LookPath(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "anyscale: anyscalectl not found:", err)
			os.Exit(1)
		}
		path = p
	}
	err := syscall.Exec(path, append([]string{"anyscalectl"}, args...), os.Environ())
	fmt.Fprintln(os.Stderr, "anyscale: run anyscalectl:", err)
	os.Exit(1)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == mix.HelperCommand {
		runHelper(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && ctlCommands[os.Args[1]] {
		runCtl(os.Args[1:])
		return
	}
	if os.Geteuid() == 0 {
		fmt.Fprintln(os.Stderr, "don't run the anyscale GUI as root; it asks for permission when it needs it")
		os.Exit(1)
	}

	defer profile()()

	locale.SanitizeEnvironment()

	po, err := fs.Sub(anyscale.Assets(), "po")
	if err != nil {
		panic(err)
	}
	locale.Init(po)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	var a ui.App
	a.Run(ctx)
}
