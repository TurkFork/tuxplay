package rtsp

import (
	"bufio"
	"fmt"
	"net"
	"strings"
)

func Start() {

	ln, err := net.Listen("tcp", ":7000")
	if err != nil {
		panic(err)
	}

	fmt.Println("RTSP server listening on port 7000")

	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}

		go handle(conn)
	}
}

func handle(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)

	var cseq string
	var request string

	for {

		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}

		line = strings.TrimSpace(line)

		if line == "" {

			// end of headers → send response
			respond(conn, request, cseq)

			request = ""
			cseq = ""
			continue
		}

		fmt.Println("AirPlay >", line)

		if strings.HasPrefix(line, "GET") ||
			strings.HasPrefix(line, "ANNOUNCE") ||
			strings.HasPrefix(line, "SETUP") ||
			strings.HasPrefix(line, "RECORD") ||
			strings.HasPrefix(line, "FLUSH") ||
			strings.HasPrefix(line, "TEARDOWN") {

			request = line
		}

		if strings.HasPrefix(line, "CSeq:") {
			cseq = strings.TrimSpace(strings.Split(line, ":")[1])
		}
	}
}

func respond(conn net.Conn, request string, cseq string) {

	if strings.Contains(request, "OPTIONS") {

		resp := fmt.Sprintf(
			"RTSP/1.0 200 OK\r\n"+
				"CSeq: %s\r\n"+
				"Public: ANNOUNCE, SETUP, RECORD, PAUSE, FLUSH, TEARDOWN, OPTIONS, GET_PARAMETER, SET_PARAMETER\r\n"+
				"\r\n",
			cseq,
		)

		conn.Write([]byte(resp))
		return
	}

	if strings.Contains(request, "GET /info") {

		body := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
"http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>deviceid</key>
    <string>48:5D:60:7C:EE:22</string>
    <key>features</key>
    <integer>119</integer>
    <key>model</key>
    <string>AppleTV3,2</string>
    <key>srcvers</key>
    <string>220.68</string>
</dict>
</plist>`

		resp := fmt.Sprintf(
			"RTSP/1.0 200 OK\r\nCSeq: %s\r\nContent-Type: text/x-apple-plist+xml\r\nContent-Length: %d\r\n\r\n%s",
			cseq,
			len(body),
			body,
		)

		conn.Write([]byte(resp))
		return
	}

	resp := fmt.Sprintf(
		"RTSP/1.0 200 OK\r\nCSeq: %s\r\n\r\n",
		cseq,
	)

	conn.Write([]byte(resp))
}
