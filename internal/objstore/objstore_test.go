package objstore_test

import (
	"os"
	"testing"

	"github.com/fuyu28/CloudLaunch_v3/internal/objstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/objstoretest"
)

func TestContract(t *testing.T) {
	cfg := objstore.Config{
		Endpoint:        os.Getenv("CLOUDLAUNCH_TEST_S3_ENDPOINT"),
		Bucket:          os.Getenv("CLOUDLAUNCH_TEST_S3_BUCKET"),
		AccessKeyID:     os.Getenv("CLOUDLAUNCH_TEST_S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("CLOUDLAUNCH_TEST_S3_SECRET_ACCESS_KEY"),
	}
	if cfg.Endpoint == "" {
		t.Skip("CLOUDLAUNCH_TEST_S3_ENDPOINT is not set")
	}
	s, err := objstore.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	objstoretest.Run(t, s)
}
