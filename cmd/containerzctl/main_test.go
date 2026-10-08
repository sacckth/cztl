package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestResourceCommandRouting(t *testing.T) {
	tests := []struct {
		command string
		args    []string
	}{
		{command: "create", args: []string{"volume", "-h"}},
		{command: "create", args: []string{"volumes", "-h"}},
		{command: "list", args: []string{"image", "-h"}},
		{command: "list", args: []string{"images", "-h"}},
		{command: "list", args: []string{"container", "-h"}},
		{command: "list", args: []string{"containers", "-h"}},
		{command: "list", args: []string{"volume", "-h"}},
		{command: "list", args: []string{"volumes", "-h"}},
		{command: "list", args: []string{"-h"}},
		{command: "remove", args: []string{"image", "-h"}},
		{command: "remove", args: []string{"images", "-h"}},
		{command: "remove", args: []string{"container", "-h"}},
		{command: "remove", args: []string{"containers", "-h"}},
		{command: "remove", args: []string{"volume", "-h"}},
		{command: "remove", args: []string{"volumes", "-h"}},
		{command: "remove", args: []string{"-h"}},
	}
	for _, test := range tests {
		t.Run(test.command+"-"+test.args[0], func(t *testing.T) {
			err := run(context.Background(), nil, test.command, test.args)
			if !errors.Is(err, flag.ErrHelp) {
				t.Fatalf("run(%q, %q) error = %v, want flag.ErrHelp", test.command, test.args, err)
			}
		})
	}
}

func TestResourceCommandsRequireKnownResource(t *testing.T) {
	for _, test := range []struct {
		command string
		args    []string
	}{
		{command: "create"},
		{command: "create", args: []string{"image"}},
		{command: "list", args: []string{"plugin"}},
		{command: "remove", args: []string{"plugin"}},
	} {
		if err := run(context.Background(), nil, test.command, test.args); err == nil {
			t.Fatalf("run(%q, %q) succeeded, want resource error", test.command, test.args)
		}
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
	err := listImages(context.Background(), nil, []string{"--limit", "-1"})
	if err == nil || !strings.Contains(err.Error(), "--limit") {
		t.Fatalf("listImages() error = %v, want invalid limit", err)
	}
}

func TestWriteImagesSkipsEmptySentinel(t *testing.T) {
	images := make(chan *containerzclient.ImageInfo, 1)
	images <- &containerzclient.ImageInfo{}
	close(images)

	var output bytes.Buffer
	if err := writeImages(&output, images); err != nil {
		t.Fatalf("writeImages(): %v", err)
	}
	if strings.Count(strings.TrimSpace(output.String()), "\n") != 0 {
		t.Fatalf("writeImages() output = %q, want header only", output.String())
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

func TestWriteVolumes(t *testing.T) {
	volumes := make(chan *containerzclient.VolumeInfo, 1)
	volumes <- &containerzclient.VolumeInfo{
		Name:         "node-exporter-proc",
		Driver:       "local",
		CreationTime: time.Date(2026, time.October, 7, 12, 0, 0, 0, time.UTC),
		Options:      map[string]string{"type": "none", "mountpoint": "/proc"},
		Labels:       map[string]string{"workload": "node-exporter"},
	}
	close(volumes)

	var output bytes.Buffer
	if err := writeVolumes(&output, volumes); err != nil {
		t.Fatalf("writeVolumes(): %v", err)
	}
	for _, value := range []string{
		"NAME",
		"DRIVER",
		"CREATED",
		"OPTIONS",
		"LABELS",
		"node-exporter-proc",
		"local",
		"2026-10-07T12:00:00Z",
		"mountpoint=/proc,type=none",
		"workload=node-exporter",
	} {
		if !strings.Contains(output.String(), value) {
			t.Fatalf("writeVolumes() output %q does not contain %q", output.String(), value)
		}
	}
}

func TestWriteVolumesSkipsEmptySentinel(t *testing.T) {
	volumes := make(chan *containerzclient.VolumeInfo, 1)
	volumes <- &containerzclient.VolumeInfo{}
	close(volumes)

	var output bytes.Buffer
	if err := writeVolumes(&output, volumes); err != nil {
		t.Fatalf("writeVolumes(): %v", err)
	}
	if strings.Count(strings.TrimSpace(output.String()), "\n") != 0 {
		t.Fatalf("writeVolumes() output = %q, want header only", output.String())
	}
}

func TestWriteVolumesReturnsStreamError(t *testing.T) {
	want := errors.New("list failed")
	volumes := make(chan *containerzclient.VolumeInfo, 1)
	volumes <- &containerzclient.VolumeInfo{Error: want}
	close(volumes)

	if err := writeVolumes(&bytes.Buffer{}, volumes); !errors.Is(err, want) {
		t.Fatalf("writeVolumes() error = %v, want %v", err, want)
	}
}
