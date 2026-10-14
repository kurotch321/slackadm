package main

import (
	"context"
	"github.com/kurotch321/slackadm/internal/cli"
	"os"
	"os/signal"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(cli.Execute(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, version))
}
