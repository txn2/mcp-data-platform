package notifyattach

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

type assetsFake map[string]*portaldomain.Asset

func (f assetsFake) Get(_ context.Context, id string) (*portaldomain.Asset, error) {
	if a, ok := f[id]; ok {
		return a, nil
	}
	return nil, errors.New("not found")
}

type versionsFake map[int]*portaldomain.AssetVersion

func (f versionsFake) GetByVersion(_ context.Context, _ string, v int) (*portaldomain.AssetVersion, error) {
	if ver, ok := f[v]; ok {
		return ver, nil
	}
	return nil, errors.New("no version")
}

// objectsFake serves objects by bucket/key and records what was read.
type objectsFake struct {
	objects map[string][]byte
	reads   []string
}

func (f *objectsFake) GetObject(_ context.Context, bucket, key string) (data []byte, contentType string, err error) {
	f.reads = append(f.reads, bucket+"/"+key)
	if b, ok := f.objects[bucket+"/"+key]; ok {
		return b, "application/octet-stream", nil
	}
	return nil, "", errors.New("NoSuchKey")
}

type resourcesFake map[string]*resource.Resource

func (f resourcesFake) Get(_ context.Context, id string) (*resource.Resource, error) {
	if r, ok := f[id]; ok {
		return r, nil
	}
	return nil, errors.New("not found")
}

func fixture() (l *Loader, assetObjs, resObjs *objectsFake) {
	assetObjs = &objectsFake{objects: map[string][]byte{
		"portal/assets/a1/v3/report.md":   []byte("current"),
		"portal/assets/a1/v2/report.md":   []byte("pinned"),
		"portal/assets/a1/.thumbnail.png": []byte("tile"),
	}}
	resObjs = &objectsFake{objects: map[string][]byte{"resources/resources/r1/raw.csv": []byte("a,b\n1,2\n")}}
	deleted := time.Now()
	l = &Loader{
		Assets: assetsFake{
			"a1": {
				ID: "a1", S3Bucket: "portal", S3Key: "assets/a1/v3/report.md", ContentType: "text/markdown",
				CurrentVersion: 3, SizeBytes: 7, ThumbnailS3Key: "assets/a1/.thumbnail.png",
			},
			"big":  {ID: "big", S3Bucket: "portal", S3Key: "k", SizeBytes: notification.MaxAttachmentBytes + 1},
			"gone": {ID: "gone", DeletedAt: &deleted},
		},
		Versions:       versionsFake{2: {S3Bucket: "portal", S3Key: "assets/a1/v2/report.md", ContentType: "text/markdown", SizeBytes: 6}},
		AssetObjects:   assetObjs,
		Resources:      resourcesFake{"r1": {ID: "r1", Filename: "raw.csv", MIMEType: "text/csv", S3Key: "resources/r1/raw.csv", SizeBytes: 8}},
		ResourceObjs:   resObjs,
		ResourceBucket: "resources",
	}
	return l, assetObjs, resObjs
}

func TestLoad_AnAssetAtItsPinnedVersion(t *testing.T) {
	l, _, _ := fixture()
	f, err := l.Load(context.Background(), notification.Attachment{AssetID: "a1", Version: 2, Name: "report.md"})
	if err != nil || string(f.Data) != "pinned" || f.Name != "report.md" || f.ContentType != "text/markdown" {
		t.Fatalf("Load = %+v, %v; want the version-2 content", f, err)
	}
}

func TestLoad_AnAssetAtItsCurrentVersion(t *testing.T) {
	l, _, _ := fixture()
	for _, v := range []int{0, 3} {
		f, err := l.Load(context.Background(), notification.Attachment{AssetID: "a1", Version: v})
		if err != nil || string(f.Data) != "current" {
			t.Errorf("version %d: Load = %q, %v; want the current content", v, f.Data, err)
		}
	}
}

func TestLoad_APreviewIsTheLightTile(t *testing.T) {
	l, _, _ := fixture()
	f, err := l.Load(context.Background(), notification.Attachment{AssetID: "a1", Preview: true, Name: "Report.png"})
	if err != nil || string(f.Data) != "tile" || f.ContentType != "image/png" {
		t.Fatalf("Load = %+v, %v; want the tile as a png", f, err)
	}
}

func TestLoad_AResource(t *testing.T) {
	l, _, resObjs := fixture()
	f, err := l.Load(context.Background(), notification.Attachment{ResourceID: "r1"})
	if err != nil || f.Name != "raw.csv" || f.ContentType != "text/csv" || string(f.Data) != "a,b\n1,2\n" {
		t.Fatalf("Load = %+v, %v", f, err)
	}
	if resObjs.reads[0] != "resources/resources/r1/raw.csv" {
		t.Errorf("read %q, want the resource bucket's key", resObjs.reads[0])
	}
}

func TestLoad_TooLargeIsRefusedBeforeItIsRead(t *testing.T) {
	l, assetObjs, _ := fixture()
	_, err := l.Load(context.Background(), notification.Attachment{AssetID: "big"})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if len(assetObjs.reads) != 0 {
		t.Error("an attachment known to be too large was read")
	}
}

func TestLoad_TooLargeAfterReading(t *testing.T) {
	l, assetObjs, _ := fixture()
	assetObjs.objects["portal/assets/a1/v3/report.md"] = make([]byte, notification.MaxAttachmentBytes+1)
	if _, err := l.Load(context.Background(), notification.Attachment{AssetID: "a1"}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestLoad_Unavailable(t *testing.T) {
	l, _, _ := fixture()
	cases := map[string]notification.Attachment{
		"deleted asset":    {AssetID: "gone"},
		"unknown asset":    {AssetID: "nope"},
		"unknown version":  {AssetID: "a1", Version: 1},
		"unknown resource": {ResourceID: "nope"},
		"nothing named":    {},
		"no preview":       {AssetID: "big", Preview: true},
	}
	for name, att := range cases {
		if _, err := l.Load(context.Background(), att); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s: err = %v, want ErrUnavailable", name, err)
		}
	}
	var nilLoader *Loader
	if _, err := nilLoader.Load(context.Background(), notification.Attachment{AssetID: "a1"}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil loader: err = %v", err)
	}
	empty := &Loader{}
	for _, att := range []notification.Attachment{{AssetID: "a1"}, {AssetID: "a1", Preview: true}, {ResourceID: "r1"}} {
		if _, err := empty.Load(context.Background(), att); !errors.Is(err, ErrUnavailable) {
			t.Errorf("no stores, %+v: err = %v", att, err)
		}
	}
}

func TestLoad_AMissingObjectIsUnavailable(t *testing.T) {
	l, _, resObjs := fixture()
	delete(resObjs.objects, "resources/resources/r1/raw.csv")
	if _, err := l.Load(context.Background(), notification.Attachment{ResourceID: "r1"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}
