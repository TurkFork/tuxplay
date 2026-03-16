package mdns

import (
	"github.com/grandcat/zeroconf"
)

func Start(name string) (*zeroconf.Server, error) {

	txt := []string{
		"deviceid=48:5D:60:7C:EE:22",
		"features=0x5A7FFFF7,0x1E",
		"model=Linux",
		"srcvers=220.68",
	}

	server, err := zeroconf.Register(
		name,
		"_airplay._tcp",
		"local.",
		7000,
		txt,
		nil,
	)

	return server, err
}
