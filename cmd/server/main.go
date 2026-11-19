// Command server is the single deployable binary:
//
//	server serve                      run the HTTP server and background worker
//	server migrate up|down|status     apply, roll back or inspect migrations
//	server seed                       apply reference data and sync permissions/roles
//	server admin create [flags]       create the first super administrator
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // IANA zones for settings validation on hosts without tzdata (e.g. Windows)

	"github.com/alumasinde/profy/internal/app"
	"github.com/alumasinde/profy/internal/config"
	"github.com/alumasinde/profy/internal/database"
)

const usage = `usage:
  server serve
  server migrate up|down|status
  server seed
  server admin create --first NAME [--middle NAME] --last NAME (--email EMAIL | --phone PHONE)
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	log := app.NewLogger(cfg)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	switch args[0] {
	case "serve":
		a, err := app.Build(cfg, log, db)
		if err != nil {
			return err
		}
		return a.Serve(ctx)

	case "migrate":
		sub := "up"
		if len(args) > 1 {
			sub = args[1]
		}
		switch sub {
		case "up":
			return database.MigrateUp(ctx, db, log)
		case "down":
			return database.MigrateDown(ctx, db, log)
		case "status":
			lines, err := database.MigrationStatus(ctx, db)
			for _, l := range lines {
				fmt.Println(l)
			}
			return err
		}
		return fmt.Errorf("unknown migrate command %q\n%s", sub, usage)

	case "seed":
		return app.Seed(ctx, db, log)

	case "admin":
		if len(args) < 2 || args[1] != "create" {
			return fmt.Errorf("unknown admin command\n%s", usage)
		}
		fs := flag.NewFlagSet("admin create", flag.ContinueOnError)
		var in app.AdminInput
		fs.StringVar(&in.FirstName, "first", "", "first name (required)")
		fs.StringVar(&in.MiddleName, "middle", "", "middle name (optional)")
		fs.StringVar(&in.LastName, "last", "", "last name (required)")
		fs.StringVar(&in.Email, "email", "", "email used to sign in with a code")
		fs.StringVar(&in.Phone, "phone", "", "Kenyan mobile used to sign in with a code")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		id, created, err := app.CreateSuperAdmin(ctx, db, in)
		if err != nil {
			return err
		}
		verb := "promoted existing user"
		if created {
			verb = "created user"
		}
		fmt.Printf("Super administrator ready (%s %s). Sign in at /auth/login with a one-time code.\n", verb, id)
		return nil
	}
	return fmt.Errorf("unknown command %q\n%s", args[0], usage)
}
