package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

type DockerInfo struct {
	ServerVersion string            `json:"server_version"`
	Containers    int               `json:"containers"`
	Running       int               `json:"running"`
	Paused        int               `json:"paused"`
	Stopped       int               `json:"stopped"`
	Images        int               `json:"images"`
	ContainerList []DockerContainer `json:"container_list"`
	Networks      []DockerNetwork   `json:"networks"`
}

type DockerContainer struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Image    string         `json:"image"`
	State    string         `json:"state"`
	Status   string         `json:"status"`
	Created  time.Time      `json:"created"`
	Ports    []DockerPort   `json:"ports"`
	Networks []ContainerNet `json:"networks"`
}

// DockerPort: PrivatePort is the port inside the container; PublicPort is the
// port published on the host (0 when only exposed internally).
type DockerPort struct {
	IP          string `json:"ip,omitempty"`
	PrivatePort int    `json:"private_port"`
	PublicPort  int    `json:"public_port,omitempty"`
	Type        string `json:"type"`
}

type ContainerNet struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
}

type DockerNetwork struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Driver     string   `json:"driver"`
	Scope      string   `json:"scope"`
	Internal   bool     `json:"internal"`
	Subnets    []string `json:"subnets"`
	Gateways   []string `json:"gateways"`
	Containers []string `json:"containers"`
}

// Docker talks to the Docker Engine API over its unix socket.
type Docker struct {
	client *http.Client
}

func NewDocker(socket string) *Docker {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}
	return &Docker{client: &http.Client{Transport: tr, Timeout: 15 * time.Second}}
}

func (d *Docker) Name() string { return "docker" }

func (d *Docker) get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("docker %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func (d *Docker) Collect(ctx context.Context) (any, error) {
	var info struct {
		ServerVersion     string
		Containers        int
		ContainersRunning int
		ContainersPaused  int
		ContainersStopped int
		Images            int
	}
	if err := d.get(ctx, "/info", &info); err != nil {
		return nil, err
	}

	var rawContainers []struct {
		ID      string `json:"Id"`
		Names   []string
		Image   string
		State   string
		Status  string
		Created int64
		Ports   []struct {
			IP          string
			PrivatePort int
			PublicPort  int
			Type        string
		}
		NetworkSettings struct {
			Networks map[string]struct {
				IPAddress string
			}
		}
	}
	if err := d.get(ctx, "/containers/json?all=1", &rawContainers); err != nil {
		return nil, err
	}

	var rawNetworks []struct {
		ID       string `json:"Id"`
		Name     string
		Driver   string
		Scope    string
		Internal bool
		IPAM     struct {
			Config []struct {
				Subnet  string
				Gateway string
			}
		}
	}
	if err := d.get(ctx, "/networks", &rawNetworks); err != nil {
		return nil, err
	}

	out := DockerInfo{
		ServerVersion: info.ServerVersion,
		Containers:    info.Containers,
		Running:       info.ContainersRunning,
		Paused:        info.ContainersPaused,
		Stopped:       info.ContainersStopped,
		Images:        info.Images,
	}

	netMembers := map[string][]string{}
	for _, rc := range rawContainers {
		c := DockerContainer{
			ID:      shortID(rc.ID),
			Image:   rc.Image,
			State:   rc.State,
			Status:  rc.Status,
			Created: time.Unix(rc.Created, 0),
		}
		if len(rc.Names) > 0 {
			c.Name = strings.TrimPrefix(rc.Names[0], "/")
		}
		// Docker reports a published port once per host IP family (0.0.0.0 and ::); keep one.
		seen := map[string]bool{}
		for _, p := range rc.Ports {
			key := fmt.Sprintf("%d/%d/%s", p.PrivatePort, p.PublicPort, p.Type)
			if seen[key] {
				continue
			}
			seen[key] = true
			c.Ports = append(c.Ports, DockerPort{IP: p.IP, PrivatePort: p.PrivatePort, PublicPort: p.PublicPort, Type: p.Type})
		}
		sort.Slice(c.Ports, func(i, j int) bool { return c.Ports[i].PrivatePort < c.Ports[j].PrivatePort })
		for name, n := range rc.NetworkSettings.Networks {
			c.Networks = append(c.Networks, ContainerNet{Name: name, IP: n.IPAddress})
			netMembers[name] = append(netMembers[name], c.Name)
		}
		sort.Slice(c.Networks, func(i, j int) bool { return c.Networks[i].Name < c.Networks[j].Name })
		out.ContainerList = append(out.ContainerList, c)
	}
	sort.Slice(out.ContainerList, func(i, j int) bool {
		a, b := out.ContainerList[i], out.ContainerList[j]
		if (a.State == "running") != (b.State == "running") {
			return a.State == "running"
		}
		return a.Name < b.Name
	})

	for _, rn := range rawNetworks {
		n := DockerNetwork{
			ID: shortID(rn.ID), Name: rn.Name, Driver: rn.Driver,
			Scope: rn.Scope, Internal: rn.Internal,
			Containers: netMembers[rn.Name],
		}
		for _, c := range rn.IPAM.Config {
			if c.Subnet != "" {
				n.Subnets = append(n.Subnets, c.Subnet)
			}
			if c.Gateway != "" {
				n.Gateways = append(n.Gateways, c.Gateway)
			}
		}
		sort.Strings(n.Containers)
		out.Networks = append(out.Networks, n)
	}
	sort.Slice(out.Networks, func(i, j int) bool { return out.Networks[i].Name < out.Networks[j].Name })
	return out, nil
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
