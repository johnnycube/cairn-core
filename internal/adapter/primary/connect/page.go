package connect

import (
	"strconv"

	v1 "github.com/johnnycube/cairn-core/gen/proto/cairn/v1"
)

// Pagination helpers. The proto PageRequest uses an opaque cursor; for
// the endpoints currently implemented the underlying repos take
// offset+limit, so we round-trip the offset as a base-10 string. When a
// future endpoint moves to keyset pagination (segment-effort listings,
// activity feeds at scale), it picks a different cursor encoding and
// stops calling these helpers.

const defaultPageLimit = 50
const maxPageLimit = 200

func pageFrom(p *v1.PageRequest) (limit, offset int) {
	limit = defaultPageLimit
	if p != nil && p.GetLimit() > 0 {
		limit = int(p.GetLimit())
		if limit > maxPageLimit {
			limit = maxPageLimit
		}
	}
	if p == nil || p.GetCursor() == "" {
		return limit, 0
	}
	off, err := strconv.Atoi(p.GetCursor())
	if err != nil || off < 0 {
		return limit, 0
	}
	return limit, off
}

func encodeOffset(off int) string {
	return strconv.Itoa(off)
}
