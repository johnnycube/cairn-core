package port

import (
	"context"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// StreamRepo persists and reads activity stream samples (1Hz time-series
// per activity_source). The underlying storage is a TimescaleDB
// hypertable with two pre-aggregated continuous aggregates (5s, 30s).
// Implementations route reads to the right table based on
// StreamQuery.Resolution.
//
// Write semantics: WriteStream is a replace — it deletes any existing
// samples for the source and inserts the new set in one transaction.
// This matches the worker→core data flow where a stream is parsed once
// per source and re-parsed on reimport (LocalReparse).
type StreamRepo interface {
	// WriteStream replaces all samples for the source. Empty samples is
	// a valid input — it deletes any existing samples without inserting
	// new ones (used by source detach).
	WriteStream(ctx context.Context, sourceID domain.SourceID, samples []domain.StreamSample) error

	// QueryStream returns samples in the requested resolution and time
	// window. Channels in the query are advisory in v1; the response
	// always returns every channel the chosen resolution stores. NULL
	// channels remain nil on the returned samples.
	QueryStream(ctx context.Context, q domain.StreamQuery) (domain.Stream, error)

	// DeleteStream removes all samples for a source. Called when the
	// parent source is detached or hard-deleted.
	DeleteStream(ctx context.Context, sourceID domain.SourceID) error

	// RefreshAggregates materialises the 5s/30s continuous aggregates for the
	// source's own time window. REQUIRED after writing a backfilled/historical
	// stream: the automatic refresh policy only covers a recent window
	// (90d/180d), so an imported activity dated months/years ago would never
	// have its downsampled buckets built — and every downsampled read (GPS
	// track, charts) would come back empty. Must run outside a transaction.
	RefreshAggregates(ctx context.Context, sourceID domain.SourceID) error

	// FirstGeoPoint returns the earliest GPS sample (lat, lon) for a source.
	// found is false when the source has no GPS-bearing samples (indoor
	// activity, no track). Used by the start-location reverse-geocoder.
	FirstGeoPoint(ctx context.Context, sourceID domain.SourceID) (lat, lon float64, found bool, err error)
}
