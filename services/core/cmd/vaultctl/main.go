// Command vaultctl is the operator CLI (migrations, tenant bootstrap, webhook
// replay). It runs from a laptop or CI, never inside a Lambda.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/vaultops/core/internal/app"
	"github.com/vaultops/core/internal/store"
)

func usage() {
	fmt.Fprint(os.Stderr, `vaultctl <command> [flags]

commands:
  migrate      apply db/migrations         (needs DATABASE_OWNER_URL)
  org-create   create an organization and print its webhook token once
  replay       re-send recorded webhook payloads to an edge URL
  audit-anchor verify every audit chain and write anchors (same as the recon task)
`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var err error
	switch os.Args[1] {
	case "migrate":
		err = migrate(ctx, os.Args[2:])
	case "org-create":
		err = orgCreate(ctx, os.Args[2:])
	case "replay":
		err = replay(ctx, os.Args[2:])
	case "audit-anchor":
		err = auditAnchor(ctx)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "vaultctl:", err)
		os.Exit(1)
	}
}

func migrate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	dir := fs.String("dir", "db/migrations", "migrations directory")
	setPw := fs.Bool("set-app-password", false, "enable LOGIN for vaultops_app using $VAULTOPS_APP_DB_PASSWORD")
	_ = fs.Parse(args)
	url := os.Getenv("DATABASE_OWNER_URL")
	if url == "" {
		return fmt.Errorf("DATABASE_OWNER_URL is required")
	}
	if err := store.Migrate(ctx, url, *dir); err != nil {
		return err
	}
	if *setPw {
		if err := store.SetAppRolePassword(ctx, url, os.Getenv("VAULTOPS_APP_DB_PASSWORD")); err != nil {
			return err
		}
	}
	fmt.Println("migrations applied")
	return nil
}

func orgCreate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("org-create", flag.ExitOnError)
	name := fs.String("name", "", "organization name")
	_ = fs.Parse(args)
	if *name == "" {
		return fmt.Errorf("--name is required")
	}
	s, err := store.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer s.Close()
	org, token, err := s.CreateOrganization(ctx, *name)
	if err != nil {
		return err
	}
	// Machine-readable; the token is not stored anywhere and cannot be shown again.
	fmt.Printf("org_id=%s\nwebhook_token=%s\n", org.ID, token)
	return nil
}

func auditAnchor(ctx context.Context) error {
	a, err := app.New(ctx, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		return err
	}
	if a.Anchor == nil {
		return fmt.Errorf("needs DATABASE_URL and ANCHOR_DIR (local) or R2_* settings")
	}
	res, err := a.Anchor.Run(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("anchored=%d problems=%d\n", res.Anchored, len(res.Problems))
	for _, p := range res.Problems {
		fmt.Println("PROBLEM:", p)
	}
	if len(res.Problems) > 0 {
		return fmt.Errorf("audit integrity problems found")
	}
	return nil
}
