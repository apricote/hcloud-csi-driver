/*
Copyright 2018 DigitalOcean

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package driver

import (
	"encoding/json"
	"github.com/hetznercloud/hcloud-go/hcloud"
	"github.com/hetznercloud/hcloud-go/hcloud/schema"
	"strconv"

	"io/ioutil"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kubernetes-csi/csi-test/pkg/sanity"
	"github.com/sirupsen/logrus"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func TestDriverSuite(t *testing.T) {
	socket := "/tmp/csi.sock"
	endpoint := "unix://" + socket
	if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
		t.Fatalf("failed to remove unix domain socket file %s, error: %s", socket, err)
	}

	serverID := 1234567
	fakeHCloud := &fakeAPI{
		t:       t,
		volumes: map[int]*schema.Volume{},
		servers: map[int]*schema.Server{
			serverID: {
				ID: serverID,
			},
		},
	}

	tsHCloud := httptest.NewServer(fakeHCloud)
	defer tsHCloud.Close()

	hcloudClient := hcloud.NewClient(hcloud.WithEndpoint(tsHCloud.URL))

	driver := &Driver{
		endpoint:     endpoint,
		nodeID:       strconv.Itoa(serverID),
		region:       "fsn1",
		hcloudClient: hcloudClient,
		mounter:      &fakeMounter{},
		log:          logrus.New().WithField("test_enabled", true),
	}
	defer driver.Stop()

	go driver.Run()

	mntDir, err := ioutil.TempDir("", "mnt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(mntDir)

	mntStageDir, err := ioutil.TempDir("", "mnt-stage")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(mntStageDir)

	cfg := &sanity.Config{
		StagingPath: mntStageDir,
		TargetPath:  mntDir,
		Address:     endpoint,
	}

	sanity.Test(t, cfg)
}

// fakeAPI implements a fake, cached Hetzner Cloud API
type fakeAPI struct {
	t       *testing.T
	volumes map[int]*schema.Volume
	servers map[int]*schema.Server
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/servers/") {
		// for now we only do a GET, so we assume it's a GET and don't check
		// for the method
		resp := new(schema.ServerGetResponse)
		id, _ := strconv.Atoi(filepath.Base(r.URL.Path))
		server, ok := f.servers[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)

			errResp := &schema.ErrorResponse{
				Error: schema.Error{
					Code: string(hcloud.ErrorCodeNotFound),
				},
			}

			err := json.NewEncoder(w).Encode(&errResp)
			if err != nil {
				f.t.Fatalf("error: %s", err)
			}
			return
		}
		resp.Server = *server

		err := json.NewEncoder(w).Encode(&resp)
		if err != nil {
			f.t.Fatalf("error: %s", err)
		}
		return
	}

	// actions always succeeded instantly
	if strings.HasPrefix(r.URL.Path, "/actions/") {
		// for now we only do a GET, so we assume it's a GET and don't check
		// for the method
		id, _ := strconv.Atoi(filepath.Base(r.URL.Path))
		resp := &schema.ActionGetResponse{
			Action: schema.Action{
				ID:     id,
				Status: string(hcloud.ActionStatusSuccess),
			},
		}

		err := json.NewEncoder(w).Encode(&resp)
		if err != nil {
			f.t.Fatalf("error: %s", err)
		}
		return
	}

	// rest is /volumes related
	switch r.Method {
	case "GET":
		// A list call
		if strings.HasPrefix(r.URL.String(), "/volumes?") {
			volumes := []schema.Volume{}
			if name := r.URL.Query().Get("name"); name != "" {
				for _, vol := range f.volumes {
					if vol.Name == name {
						volumes = append(volumes, *vol)
					}
				}
			} else {
				for _, vol := range f.volumes {
					volumes = append(volumes, *vol)
				}
			}

			resp := new(schema.VolumeListResponse)
			resp.Volumes = volumes

			err := json.NewEncoder(w).Encode(&resp)
			if err != nil {
				f.t.Fatal(err)
			}
			return

		} else {
			resp := new(schema.VolumeGetResponse)
			// single volume get
			id, _ := strconv.Atoi(filepath.Base(r.URL.Path))
			vol, ok := f.volumes[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
			} else {
				resp.Volume = *vol
			}

			_ = json.NewEncoder(w).Encode(&resp)
			return
		}

	case "POST":
		v := new(schema.VolumeCreateRequest)
		err := json.NewDecoder(r.Body).Decode(v)
		if err != nil {
			f.t.Fatal(err)
		}

		id := rand.Int()
		vol := &schema.Volume{
			ID:      id,
			Name:    v.Name,
			Size:    v.Size,
			Created: time.Now().UTC(),
		}

		f.volumes[id] = vol

		resp := &schema.VolumeCreateResponse{
			Volume: *vol,
		}

		err = json.NewEncoder(w).Encode(&resp)
		if err != nil {
			f.t.Fatal(err)
		}
	case "DELETE":
		id, _ := strconv.Atoi(filepath.Base(r.URL.Path))
		delete(f.volumes, id)
	}
}

type fakeMounter struct{}

func (f *fakeMounter) Format(source string, fsType string) error {
	return nil
}

func (f *fakeMounter) Mount(source string, target string, fsType string, options ...string) error {
	return nil
}

func (f *fakeMounter) Unmount(target string) error {
	return nil
}

func (f *fakeMounter) IsFormatted(source string) (bool, error) {
	return true, nil
}
func (f *fakeMounter) IsMounted(target string) (bool, error) {
	return true, nil
}
