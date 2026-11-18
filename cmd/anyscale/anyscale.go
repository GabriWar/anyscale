package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"runtime/pprof"

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

func main() {
	if len(os.Args) > 1 && os.Args[1] == mix.HelperCommand {
		runHelper(os.Args[2:])
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
