package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	containerzclient "github.com/openconfig/containerz/client"
	commonpb "github.com/openconfig/gnoi/common"
	containerzpb "github.com/openconfig/gnoi/containerz"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

func TestWritePullResponseSuccess(t *testing.T) {
	var output bytes.Buffer
	done, err := writePullResponse(&output, &containerzpb.DeployResponse{
		Response: &containerzpb.DeployResponse_ImageTransferSuccess{
			ImageTransferSuccess: &containerzpb.ImageTransferSuccess{
				Name: "example/app",
				Tag:  "1.0",
			},
		},
	})
	if err != nil {
		t.Fatalf("writePullResponse(): %v", err)
	}
	if !done {
		t.Fatal("writePullResponse() did not mark success as done")
	}
	if got := output.String(); !strings.Contains(got, "pulled example/app:1.0") {
		t.Fatalf("writePullResponse() output = %q", got)
	}
}

func TestPullImageRequiresImage(t *testing.T) {
	err := pullImage(context.Background(), nil, nil)
	if err == nil || err.Error() != "--image is required" {
		t.Fatalf("pullImage() error = %v, want --image is required", err)
	}
}

func TestPullImageRequiresURL(t *testing.T) {
	err := pullImage(context.Background(), nil, []string{"--image", "example/app"})
	if err == nil || err.Error() != "--url is required" {
		t.Fatalf("pullImage() error = %v, want --url is required", err)
	}
}

func TestResolveRemoteArchiveInfersHTTPSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodHead {
			t.Errorf("request method = %s, want HEAD", request.Method)
		}
		w.Header().Set("Content-Length", "60")
	}))
	defer server.Close()

	got, err := resolveRemoteArchive(context.Background(), server.URL+"/image.tar", "auto", 0)
	if err != nil {
		t.Fatalf("resolveRemoteArchive(): %v", err)
	}
	if got.size != 60 {
		t.Fatalf("resolveRemoteArchive() size = %d, want 60", got.size)
	}
	if got.protocol != commonpb.RemoteDownload_HTTP {
		t.Fatalf("resolveRemoteArchive() protocol = %s, want HTTP", got.protocol)
	}
}

func TestResolveRemoteArchiveRequiresSizeForSFTP(t *testing.T) {
	if _, err := resolveRemoteArchive(context.Background(), "host:/image.tar", "sftp", 0); err == nil {
		t.Fatal("resolveRemoteArchive() accepted SFTP without --image-size")
	}
}

func TestWritePullResponseReturnsTargetError(t *testing.T) {
	response := &containerzpb.DeployResponse{
		Response: &containerzpb.DeployResponse_ImageTransferError{
			ImageTransferError: status.New(codes.InvalidArgument, "pull failed").Proto(),
		},
	}
	done, err := writePullResponse(&bytes.Buffer{}, response)
	if done {
		t.Fatal("writePullResponse() marked an error as done")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("writePullResponse() error = %v, want InvalidArgument", err)
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
