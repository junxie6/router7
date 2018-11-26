// Copyright 2018 Google Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package netconfig

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"syscall"

	"github.com/mdlayher/genetlink"
	"github.com/rtr7/router7/internal/wg"
	"github.com/vishvananda/netlink"
)

type wireguardPeer struct {
	PublicKey  string   `json:"public_key"`  // base64-encoded
	Endpoint   string   `json:"endpoint"`    // e.g. “[::1]:12345”
	AllowedIPs []string `json:"allowed_ips"` // e.g. “["fe80::/64", "10.0.137.0/24"]”
}

type wireguardInterface struct {
	Name       string          `json:"name"`        // e.g. “wg0”
	PrivateKey string          `json:"private_key"` // base64-encoded
	Port       int             `json:"port"`        // e.g. “51820”
	Peers      []wireguardPeer `json:"peers"`
}

type wireguardInterfaces struct {
	Interfaces []wireguardInterface `json:"interfaces"`
}

type wgLink struct {
	name string
}

func (w *wgLink) Type() string {
	return "wireguard"
}

func (w *wgLink) Attrs() *netlink.LinkAttrs {
	attrs := netlink.NewLinkAttrs()
	attrs.Name = w.name
	return &attrs
}

func applyWireGuard(dir string) error {
	b, err := ioutil.ReadFile(filepath.Join(dir, "wireguard.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var cfg wireguardInterfaces
	if err := json.Unmarshal(b, &cfg); err != nil {
		return err
	}

	h, err := netlink.NewHandle()
	if err != nil {
		return fmt.Errorf("netlink.NewHandle: %v", err)
	}
	defer h.Delete()

	conn, err := genetlink.Dial(nil)
	if err != nil {
		return fmt.Errorf("genetlink.Dial: %v", err)
	}
	defer conn.Close()

	for _, iface := range cfg.Interfaces {
		l := &wgLink{iface.Name}
		if err := h.LinkAdd(l); err != nil {
			if ee, ok := err.(syscall.Errno); !ok || ee != syscall.EEXIST {
				return fmt.Errorf("LinkAdd(%v): %v", l, err)
			}
		}

		var peers []*wg.Peer
		for _, p := range iface.Peers {
			var ips []*net.IPNet
			for _, ip := range p.AllowedIPs {
				_, ipnet, err := net.ParseCIDR(ip)
				if err != nil {
					return err
				}

				ips = append(ips, ipnet)
			}
			b, err := base64.StdEncoding.DecodeString(p.PublicKey)
			if err != nil {
				return err
			}
			peers = append(peers, &wg.Peer{
				PublicKey:  b,
				Endpoint:   p.Endpoint,
				AllowedIPs: ips,
			})
		}
		b, err := base64.StdEncoding.DecodeString(iface.PrivateKey)
		if err != nil {
			return err
		}
		d := &wg.Device{
			Ifname:     iface.Name,
			PrivateKey: b,
			ListenPort: uint16(iface.Port),
			Peers:      peers,
		}
		if err := wg.SetDevice(conn, d); err != nil {
			return err
		}
	}

	return nil
}
