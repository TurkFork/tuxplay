package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"
)

func handleConnection(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}

		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		fmt.Println("RTSP:", line)

		if strings.Contains(line, "SET_PARAMETER") {
			fmt.Println("Control message received")
		}

		if strings.Contains(line, "FLUSH") {
			fmt.Println("Skip / flush command")
		}

		if strings.Contains(line, "TEARDOWN") {
			fmt.Println("Stream stopped")
		}
	}
}

func main() {

	port := ":7000"

	ln, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("AirBridge listening on", port)

	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}

		go handleConnection(conn)
	}
}