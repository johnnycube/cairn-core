package s3

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/johnnycube/cairn-core/internal/config"
	"github.com/johnnycube/cairn-core/internal/port"
)

func testStore(t *testing.T) *BlobStore {
	t.Helper()
	bs, err := New(config.StorageConfig{
		Endpoint:        "http://minio:9000",
		Region:          "us-east-1",
		Bucket:          "cairn",
		AccessKeyID:     "test",
		SecretAccessKey: "test",
		UsePathStyle:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return bs
}

// The wire contract carries hex; the x-amz-checksum-sha256 header must get
// base64 of the raw digest — MinIO 400s a hex value there (bit prod on the
// first claim-checked result).
func TestPresignUpload_ChecksumHeaderIsBase64(t *testing.T) {
	bs := testStore(t)

	digest := sha256.Sum256([]byte("payload"))
	hexSHA := hex.EncodeToString(digest[:])
	wantB64 := base64.StdEncoding.EncodeToString(digest[:])

	signed, err := bs.PresignUpload(context.Background(), "transfer/strava/x.json", port.PresignUploadOpts{
		ContentType:   "application/json",
		ContentSHA256: hexSHA,
	})
	if err != nil {
		t.Fatalf("PresignUpload: %v", err)
	}
	if got := signed.RequiredHeaders["x-amz-checksum-sha256"]; got != wantB64 {
		t.Errorf("required checksum header = %q; want base64 %q", got, wantB64)
	}
	for k, v := range signed.RequiredHeaders {
		if strings.EqualFold(k, "x-amz-checksum-sha256") && v == hexSHA {
			t.Errorf("hex digest leaked into required header %q", k)
		}
	}
}

func TestPresignUpload_RejectsNonHexChecksum(t *testing.T) {
	bs := testStore(t)
	_, err := bs.PresignUpload(context.Background(), "k", port.PresignUploadOpts{
		ContentType:   "application/json",
		ContentSHA256: "not-hex!",
	})
	if err == nil {
		t.Fatal("want error for non-hex checksum")
	}
}
