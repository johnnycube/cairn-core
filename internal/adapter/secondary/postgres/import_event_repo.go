package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// ImportEventRepo implements port.ImportEventRepo over connection_import_events.
type ImportEventRepo struct {
	pool *pgxpool.Pool
}

func NewImportEventRepo(pool *pgxpool.Pool) *ImportEventRepo {
	return &ImportEventRepo{pool: pool}
}

func (r *ImportEventRepo) Record(ctx context.Context, ev domain.ConnectionImportEvent) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO connection_import_events
		    (external_account_id, kind, count, detail, external_id)
		 VALUES ($1, $2, $3, $4, $5)`,
		ev.ExternalAccountID.UUID(), ev.Kind, ev.Count, ev.Detail, ev.ExternalID)
	if err != nil {
		return fmt.Errorf("record import event: %w", err)
	}
	return nil
}

func (r *ImportEventRepo) ListForAccount(
	ctx context.Context,
	accountID domain.ExternalAccountID,
	limit int,
) ([]domain.ConnectionImportEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, external_account_id, kind, count, detail, external_id, occurred_at
		   FROM connection_import_events
		  WHERE external_account_id = $1
		  ORDER BY occurred_at DESC
		  LIMIT $2`,
		accountID.UUID(), limit)
	if err != nil {
		return nil, fmt.Errorf("list import events: %w", err)
	}
	defer rows.Close()

	var out []domain.ConnectionImportEvent
	for rows.Next() {
		var (
			ev      domain.ConnectionImportEvent
			id, acc [16]byte
		)
		if err := rows.Scan(&id, &acc, &ev.Kind, &ev.Count, &ev.Detail, &ev.ExternalID, &ev.OccurredAt); err != nil {
			return nil, fmt.Errorf("scan import event: %w", err)
		}
		ev.ID = domain.ConnectionImportEventID(id)
		ev.ExternalAccountID = domain.ExternalAccountID(acc)
		out = append(out, ev)
	}
	return out, rows.Err()
}
