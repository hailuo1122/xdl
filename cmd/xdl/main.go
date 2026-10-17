package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/xdl-app/xdl/internal/app"
	"github.com/xdl-app/xdl/internal/engine"
	"github.com/xdl-app/xdl/internal/nativehost"
)

func main() {
	// The installed native-host copy is the same binary under a different
	// name. Chrome launches it with the extension origin as argv[1] and no
	// flags; flags such as --minimized still mean "run the GUI".
	base := strings.ToLower(filepath.Base(os.Args[0]))
	if strings.HasPrefix(base, "xdl-native-host") && isNativeHostInvocation(os.Args[1:]) {
		origin := ""
		if len(os.Args) > 1 {
			origin = os.Args[1]
		}
		os.Exit(nativehost.Run("", origin))
	}

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--native-host":
			origin := ""
			if len(os.Args) > 2 {
				origin = os.Args[2]
			}
			os.Exit(nativehost.Run("", origin))
		case "--install-native-host":
			os.Exit(runInstallNativeHost())
		case "--uninstall-native-host":
			os.Exit(runUninstallNativeHost())
		case "cli":
			os.Exit(runCLI(os.Args[2:]))
		case "--minimized":
			os.Exit(app.RunGUI(true))
		case "--headless":
			os.Exit(app.RunHeadless())
		case "--version":
			fmt.Println(app.Version)
			return
		}
	}
	os.Exit(app.RunGUI(false))
}

func isNativeHostInvocation(args []string) bool {
	if len(args) == 0 {
		return true
	}
	// Chromium passes the extension origin followed by a parent window handle
	// on Windows, so any leading extension origin means native-messaging mode.
	return strings.HasPrefix(args[0], "chrome-extension://")
}

func runInstallNativeHost() int {
	if err := app.RepairNativeHost(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("native host installed")
	return 0
}

func runUninstallNativeHost() int {
	if err := app.UninstallNativeHost(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("native host uninstalled")
	return 0
}

func runCLI(args []string) int {
	fs := flag.NewFlagSet("cli", flag.ExitOnError)
	outDir := fs.String("d", ".", "output directory")
	conns := fs.Int("x", engine.DefaultConns, "connections")
	name := fs.String("o", "", "output filename")
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: xdl cli [-d dir] [-x conns] [-o name] <url>")
		return 2
	}
	url := fs.Arg(0)

	dl := engine.NewHTTPDownloader()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	start := time.Now()
	res, err := dl.Download(ctx, engine.DownloadOptions{
		URL:   url,
		Dir:   *outDir,
		Name:  *name,
		Conns: *conns,
		OnProgress: func(done, total, speed int64) {
			if total > 0 {
				pct := float64(done) * 100 / float64(total)
				fmt.Printf("\r%.1f%%  %s / %s  %s/s    ", pct, human(done), human(total), human(speed))
			} else {
				fmt.Printf("\r%s  %s/s    ", human(done), human(speed))
			}
		},
	})
	fmt.Println()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	fmt.Printf("done: %s (%s) in %s\n", res.Path, human(res.Size), time.Since(start).Round(time.Millisecond))
	return 0
}

func human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
