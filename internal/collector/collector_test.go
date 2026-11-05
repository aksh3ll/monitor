package collector

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseIptablesS(t *testing.T) {
	out := `-P INPUT DROP
-P FORWARD DROP
-P OUTPUT ACCEPT
-N DOCKER
-A INPUT -i lo -j ACCEPT
-A INPUT -p tcp -m tcp --dport 22 -j ACCEPT
-A INPUT -s 10.0.0.0/8 -p tcp -m multiport --dports 80,443 -m comment --comment "web \"public\"" -j ACCEPT
-A INPUT -p udp -m udp ! --dport 53 -j DROP
-A INPUT -p tcp -m tcp --dport 6000:6010 -j REJECT --reject-with icmp-port-unreachable
-A DOCKER -d 172.17.0.2/32 ! -i docker0 -o docker0 -p tcp -m tcp --dport 80 -j ACCEPT
`
	var info IptablesInfo
	parseIptablesS("ipv4", out, &info)

	if len(info.Policies) != 3 || info.Policies[0].Policy != "DROP" {
		t.Fatalf("policies: %+v", info.Policies)
	}
	if len(info.Rules) != 6 {
		t.Fatalf("rules: got %d", len(info.Rules))
	}
	want := []FirewallPort{
		{Family: "ipv4", Chain: "INPUT", Ports: "22", Protocol: "tcp", Target: "ACCEPT"},
		{Family: "ipv4", Chain: "INPUT", Ports: "80,443", Protocol: "tcp", Target: "ACCEPT", Source: "10.0.0.0/8"},
		{Family: "ipv4", Chain: "INPUT", Ports: "!53", Protocol: "udp", Target: "DROP"},
		{Family: "ipv4", Chain: "INPUT", Ports: "6000-6010", Protocol: "tcp", Target: "REJECT"},
		{Family: "ipv4", Chain: "DOCKER", Ports: "80", Protocol: "tcp", Target: "ACCEPT", Dest: "172.17.0.2/32", InIface: "!docker0"},
	}
	if len(info.OpenPorts) != len(want) {
		t.Fatalf("open ports: got %+v", info.OpenPorts)
	}
	for i := range want {
		if info.OpenPorts[i] != want[i] {
			t.Errorf("port %d:\n got  %+v\n want %+v", i, info.OpenPorts[i], want[i])
		}
	}
}

func TestDecodeAddr(t *testing.T) {
	cases := map[string]string{
		"0100007F":                         "127.0.0.1",
		"00000000":                         "0.0.0.0",
		"00000000000000000000000000000000": "::",
		"00000000000000000000000001000000": "::1",
		"0000000000000000FFFF00000100007F": "::ffff:127.0.0.1",
	}
	for in, want := range cases {
		if got := decodeAddr(in, len(in) == 32); got != want {
			t.Errorf("decodeAddr(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestDockerCollect(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "docker.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/info", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"ServerVersion":"27.0.1","Containers":2,"ContainersRunning":1,"ContainersStopped":1,"Images":5}`))
	})
	mux.HandleFunc("/containers/json", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("all") != "1" {
			t.Errorf("expected all=1")
		}
		w.Write([]byte(`[
		 {"Id":"aaaaaaaaaaaaaaaaaaaa","Names":["/web"],"Image":"nginx","State":"running","Status":"Up 2 hours","Created":1700000000,
		  "Ports":[{"IP":"0.0.0.0","PrivatePort":80,"PublicPort":8080,"Type":"tcp"},{"IP":"::","PrivatePort":80,"PublicPort":8080,"Type":"tcp"},{"PrivatePort":443,"Type":"tcp"}],
		  "NetworkSettings":{"Networks":{"bridge":{"IPAddress":"172.17.0.2"}}}},
		 {"Id":"bbbbbbbbbbbbbbbbbbbb","Names":["/db"],"Image":"postgres","State":"exited","Status":"Exited (0)","Created":1700000000,"Ports":[],
		  "NetworkSettings":{"Networks":{}}}]`))
	})
	mux.HandleFunc("/networks", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`[{"Id":"cccccccccccccccccc","Name":"bridge","Driver":"bridge","Scope":"local","IPAM":{"Config":[{"Subnet":"172.17.0.0/16","Gateway":"172.17.0.1"}]}}]`))
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	data, err := NewDocker(sock).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	d := data.(DockerInfo)
	if d.ServerVersion != "27.0.1" || len(d.ContainerList) != 2 {
		t.Fatalf("unexpected: %+v", d)
	}
	web := d.ContainerList[0]
	if web.Name != "web" || web.ID != "aaaaaaaaaaaa" {
		t.Errorf("container: %+v", web)
	}
	if len(web.Ports) != 2 || web.Ports[0].PublicPort != 8080 || web.Ports[1].PublicPort != 0 {
		t.Errorf("ports (dedup/sort): %+v", web.Ports)
	}
	if len(d.Networks) != 1 || d.Networks[0].Subnets[0] != "172.17.0.0/16" || d.Networks[0].Containers[0] != "web" {
		t.Errorf("networks: %+v", d.Networks)
	}
}

func TestParseListUnits(t *testing.T) {
	out := `cron.service                 loaded    active   running Regular background program processing daemon
● nginx.service              loaded    failed   failed  A high performance web server
ssh.service                  loaded    inactive dead    OpenBSD Secure Shell server
`
	units := parseListUnits(out)
	want := []SystemdUnit{
		{Name: "cron.service", Load: "loaded", Active: "active", Sub: "running", Description: "Regular background program processing daemon"},
		{Name: "nginx.service", Load: "loaded", Active: "failed", Sub: "failed", Description: "A high performance web server"},
		{Name: "ssh.service", Load: "loaded", Active: "inactive", Sub: "dead", Description: "OpenBSD Secure Shell server"},
	}
	if len(units) != len(want) {
		t.Fatalf("units: got %+v", units)
	}
	for i := range want {
		if units[i] != want[i] {
			t.Errorf("unit %d:\n got  %+v\n want %+v", i, units[i], want[i])
		}
	}

	patterns := unitPatterns([]string{"cron", "nope", "docker*", " ", "app.socket"})
	if got := strings.Join(patterns, ","); got != "cron.service,nope.service,docker*.service,app.socket" {
		t.Errorf("unitPatterns = %s", got)
	}
	all := addMissingUnits(units, patterns)
	if len(all) != 5 || all[3].Name != "nope.service" || all[3].Load != "not-found" || all[4].Name != "app.socket" {
		t.Errorf("addMissingUnits: %+v", all[3:])
	}
}
