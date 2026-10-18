package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/arvlas/calbot/internal/config"
	"github.com/arvlas/calbot/internal/domain"
	"github.com/rs/zerolog/log"
)

// defaultTokenName labels a token issued without an explicit name.
const defaultTokenName = "cli"

// runUser creates users. There is no signup flow, so this is the only way in.
func runUser(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) != 1 || args[0] != "create" {
		return usage()
	}

	c, err := build(ctx, cfg)
	if err != nil {
		return err
	}
	defer c.close()

	user, err := c.users.Create(ctx)
	if err != nil {
		return err
	}

	fmt.Println(user.ID)
	return nil
}

// runToken issues and revokes API tokens. The token goes to stdout so it can be
// captured, and is never stored in plain text.
func runToken(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) < 2 {
		return usage()
	}

	id, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		return fmt.Errorf("parse id %q: %w", args[1], err)
	}

	c, err := build(ctx, cfg)
	if err != nil {
		return err
	}
	defer c.close()

	switch args[0] {
	case "issue":
		name := defaultTokenName
		if len(args) > 2 {
			name = args[2]
		}

		token, err := domain.NewToken()
		if err != nil {
			return err
		}

		tokenID, err := c.tokens.Issue(ctx, id, name, domain.HashToken(token))
		if err != nil {
			return err
		}

		log.Info().Int64("token_id", tokenID).Msg("token issued; it is shown once")
		fmt.Println(token)
		return nil
	case "revoke":
		return c.tokens.Revoke(ctx, id)
	}

	return usage()
}
