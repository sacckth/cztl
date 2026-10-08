package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	containerzclient "github.com/openconfig/containerz/client"
)

func TestKeyValueMap(t *testing.T) {
	got, err := keyValueMap([]string{"type=none", "options=bind", "mountpoint=/proc"})
	if err != nil {
		t.Fatalf("keyValueMap(): %v", err)
	}
	want := map[string]string{"type": "none", "options": "bind", "mountpoint": "/proc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keyValueMap() = %v, want %v", got, want)
	}
}

func TestKeyValueMapRejectsInvalidValue(t *testing.T) {
	if _, err := keyValueMap([]string{"invalid"}); err == nil {
		t.Fatal("keyValueMap() accepted a value without '='")
	}
}

func TestStringList(t *testing.T) {
	var values stringList
	for _, value := range []string{"one", "two"} {
		if err := values.Set(value); err != nil {
			t.Fatalf("Set(%q): %v", value, err)
		}
	}
	if got, want := values.String(), "one,two"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestWritePullProgress(t *testing.T) {
	progress := make(chan *containerzclient.Progress, 2)
	progress <- &containerzclient.Progress{BytesReceived: 10}
	progress <- &containerzclient.Progress{BytesReceived: 20}
	close(progress)

	var output bytes.Buffer
	if err := writePullProgress(&output, progress, "example/app", "1.0"); err != nil {
		t.Fatalf("writePullProgress(): %v", err)
	}
	if got := output.String(); !strings.Contains(got, "pulled example/app:1.0") {
		t.Fatalf("writePullProgress() output = %q", got)
	}
}

func TestPullImageRequiresImage(t *testing.T) {
	err := pullImage(context.Background(), nil, nil)
	if err == nil || err.Error() != "--image is required" {
		t.Fatalf("pullImage() error = %v, want --image is required", err)
	}
}

func TestWritePullProgressReturnsStreamError(t *testing.T) {
	want := errors.New("pull failed")
	progress := make(chan *containerzclient.Progress, 1)
	progress <- &containerzclient.Progress{Error: want}
	close(progress)

	if err := writePullProgress(&bytes.Buffer{}, progress, "example/app", "1.0"); !errors.Is(err, want) {
		t.Fatalf("writePullProgress() error = %v, want %v", err, want)
	}
}

func TestWriteImages(t *testing.T) {
	images := make(chan *containerzclient.ImageInfo, 1)
	images <- &containerzclient.ImageInfo{ID: "sha256:123", ImageName: "example/app", ImageTag: "1.0"}
	close(images)

	var output bytes.Buffer
	if err := writeImages(&output, images); err != nil {
		t.Fatalf("writeImages(): %v", err)
	}
	for _, value := range []string{"ID", "NAME", "TAG", "sha256:123", "example/app", "1.0"} {
		if !strings.Contains(output.String(), value) {
			t.Fatalf("writeImages() output %q does not contain %q", output.String(), value)
		}
	}
}

func TestListImagesRejectsInvalidLimit(t *testing.T) {
	err := listImages(context.Background(), nil, []string{"--limit", "-2"})
	if err == nil || !strings.Contains(err.Error(), "--limit") {
		t.Fatalf("listImages() error = %v, want invalid limit", err)
	}
}

func TestWriteImagesReturnsStreamError(t *testing.T) {
	want := errors.New("list failed")
	images := make(chan *containerzclient.ImageInfo, 1)
	images <- &containerzclient.ImageInfo{Error: want}
	close(images)

	if err := writeImages(&bytes.Buffer{}, images); !errors.Is(err, want) {
		t.Fatalf("writeImages() error = %v, want %v", err, want)
	}
}
