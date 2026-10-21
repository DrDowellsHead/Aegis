package cli

import (
	"context"

	"github.com/DrDowellsHead/Aegis/internal/secrets"
	"github.com/DrDowellsHead/Aegis/internal/storage"
)

// openConfiguredDatabase centralizes DB credentials for all server commands.
func openConfiguredDatabase(ctx context.Context) (*storage.Postgres, error) {
	dsn, err := secrets.Resolve("AEGIS_DATABASE_URL")
	if err != nil {
		return nil, err
	}
	return storage.Open(ctx, dsn)
}
