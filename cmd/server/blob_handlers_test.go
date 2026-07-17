package main

import "testing"

func TestBlobKey(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		userID   string
		sourceID string
		blobID   string
		want     string
	}{
		{
			name:     "user+source bound",
			provider: "strava",
			userID:   "u-1",
			sourceID: "s-2",
			blobID:   "b-3",
			want:     "users/u-1/strava/sources/s-2/b-3",
		},
		{
			name:     "user only (pre-source upload)",
			provider: "manual",
			userID:   "u-1",
			sourceID: "",
			blobID:   "b-3",
			want:     "users/u-1/manual/uploads/b-3",
		},
		{
			name:     "shared (no user yet — e.g. signup-time upload)",
			provider: "fit",
			userID:   "",
			sourceID: "",
			blobID:   "b-3",
			want:     "shared/fit/b-3",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := blobKey(c.provider, c.userID, c.sourceID, c.blobID)
			if got != c.want {
				t.Errorf("blobKey(%q,%q,%q,%q) = %q; want %q",
					c.provider, c.userID, c.sourceID, c.blobID, got, c.want)
			}
		})
	}
}
