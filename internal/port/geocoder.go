package port

import (
	"context"
	"errors"
)

// Place is the result of reverse-geocoding a coordinate. Name is the most
// specific populated-place name available (city → town → village → …); it is
// empty when the coordinate resolves to no usable place.
type Place struct {
	Name    string
	Country string
}

// ErrNoPlace signals that a coordinate resolved to no usable place name. It is
// distinct from a transport error: callers should treat ErrNoPlace as a final
// answer (mark attempted), but a transport error as retryable.
var ErrNoPlace = errors.New("geocoder: no place for coordinate")

// Geocoder reverse-geocodes coordinates to place names. Implementations MUST
// enforce their provider's usage policy internally (rate limiting, an
// identifying User-Agent, result caching) so callers can loop over many
// coordinates without managing throttling themselves.
type Geocoder interface {
	ReverseGeocode(ctx context.Context, lat, lon float64) (Place, error)
}
