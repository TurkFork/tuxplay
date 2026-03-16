package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"

	"tuxplay/internal/daemon"
	"tuxplay/internal/model"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	socketPath := daemon.SocketPath()

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "daemon":
		runDaemon(socketPath, logger)
	case "list":
		withClient(socketPath, func(client *http.Client) error {
			var response struct {
				Devices []model.Device `json:"devices"`
			}
			if err := doJSON(client, http.MethodGet, "/v1/devices", nil, &response); err != nil {
				return err
			}
			printDevices(response.Devices)
			return nil
		})
	case "status":
		withClient(socketPath, func(client *http.Client) error {
			var status model.Status
			if err := doJSON(client, http.MethodGet, "/v1/status", nil, &status); err != nil {
				return err
			}
			printStatus(status)
			return nil
		})
	case "route":
		if len(args) == 0 {
			exitErr("usage: tuxplay route [--add] <device>")
		}
		add := false
		if args[0] == "--add" {
			add = true
			args = args[1:]
		}
		if len(args) != 1 {
			exitErr("usage: tuxplay route [--add] <device>")
		}
		withClient(socketPath, func(client *http.Client) error {
			return doJSON(client, http.MethodPost, "/v1/route", map[string]any{
				"device": args[0],
				"add":    add,
			}, nil)
		})
	case "unroute":
		requireArgs(args, 1, "usage: tuxplay unroute <device>")
		withClient(socketPath, func(client *http.Client) error {
			return doJSON(client, http.MethodPost, "/v1/unroute", map[string]any{"device": args[0]}, nil)
		})
	case "volume":
		requireArgs(args, 2, "usage: tuxplay volume <device> <percent>")
		percent, err := daemon.ParsePercent(args[1])
		if err != nil {
			exitErr(err.Error())
		}
		withClient(socketPath, func(client *http.Client) error {
			return doJSON(client, http.MethodPost, "/v1/volume", map[string]any{
				"device":  args[0],
				"percent": percent,
			}, nil)
		})
	case "mute":
		requireArgs(args, 1, "usage: tuxplay mute <device>")
		withClient(socketPath, func(client *http.Client) error {
			return doJSON(client, http.MethodPost, "/v1/mute", map[string]any{"device": args[0]}, nil)
		})
	case "pause":
		requireArgs(args, 1, "usage: tuxplay pause <device>")
		withClient(socketPath, func(client *http.Client) error {
			return doJSON(client, http.MethodPost, "/v1/pause", map[string]any{"device": args[0]}, nil)
		})
	case "resume":
		requireArgs(args, 1, "usage: tuxplay resume <device>")
		withClient(socketPath, func(client *http.Client) error {
			return doJSON(client, http.MethodPost, "/v1/resume", map[string]any{"device": args[0]}, nil)
		})
	case "stop":
		requireArgs(args, 1, "usage: tuxplay stop <device>")
		withClient(socketPath, func(client *http.Client) error {
			return doJSON(client, http.MethodPost, "/v1/stop", map[string]any{"device": args[0]}, nil)
		})
	case "group":
		runGroup(socketPath, args)
	default:
		exitErr("unknown command: " + command)
	}
}

func runDaemon(socketPath string, logger *slog.Logger) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	server, err := daemon.New(socketPath, logger)
	if err != nil {
		exitErr(err.Error())
	}
	if err := server.Run(ctx); err != nil {
		exitErr(err.Error())
	}
}

func runGroup(socketPath string, args []string) {
	if len(args) < 2 {
		exitErr("usage: tuxplay group <create|play|add|remove> ...")
	}

	switch args[0] {
	case "create":
		if len(args) < 3 {
			exitErr("usage: tuxplay group create <name> <device> [device...]")
		}
		name := args[1]
		devices := args[2:]
		withClient(socketPath, func(client *http.Client) error {
			return doJSON(client, http.MethodPost, "/v1/group/create", map[string]any{
				"name":    name,
				"devices": devices,
			}, nil)
		})
	case "play":
		if len(args) != 2 {
			exitErr("usage: tuxplay group play <name>")
		}
		withClient(socketPath, func(client *http.Client) error {
			return doJSON(client, http.MethodPost, "/v1/group/play", map[string]any{"name": args[1]}, nil)
		})
	case "add":
		if len(args) != 3 {
			exitErr("usage: tuxplay group add <name> <device>")
		}
		withClient(socketPath, func(client *http.Client) error {
			return doJSON(client, http.MethodPost, "/v1/group/add", map[string]any{
				"name":   args[1],
				"device": args[2],
			}, nil)
		})
	case "remove":
		if len(args) != 3 {
			exitErr("usage: tuxplay group remove <name> <device>")
		}
		withClient(socketPath, func(client *http.Client) error {
			return doJSON(client, http.MethodPost, "/v1/group/remove", map[string]any{
				"name":   args[1],
				"device": args[2],
			}, nil)
		})
	default:
		exitErr("unknown group command: " + args[0])
	}
}

func withClient(socketPath string, fn func(*http.Client) error) {
	if !daemon.DaemonReachable(socketPath) {
		exitErr("tuxplay daemon is not running")
	}
	if err := fn(daemon.HTTPClient(socketPath)); err != nil {
		exitErr(err.Error())
	}
}

func doJSON(client *http.Client, method string, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, "http://unix"+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var apiErr map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&apiErr); err == nil && apiErr["error"] != "" {
			return fmt.Errorf("%s", apiErr["error"])
		}
		return fmt.Errorf("request failed: %s", resp.Status)
	}

	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func printDevices(devices []model.Device) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tMODEL\tADDRESS\tPROTO\tPW SINK\tBACKEND\tLAST SEEN")
	for _, device := range devices {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			device.Name,
			emptyDash(device.Model),
			emptyDash(device.Address),
			strings.Join(device.Protocols, ","),
			pwSinkCell(device),
			emptyDash(device.PipeWireSinkBackend),
			device.LastSeen.Local().Format("2006-01-02 15:04:05"),
		)
	}
	_ = tw.Flush()
}

func printStatus(status model.Status) {
	fmt.Printf("Socket: %s\n", status.SocketPath)
	fmt.Printf("State: %s\n", status.StatePath)
	fmt.Printf("Discovery: %t\n", status.DiscoveryLive)
	fmt.Printf("PipeWire Backend: %s\n", status.PipeWire.Backend)
	fmt.Printf("TuxPlay Sink: %s (%s)\n", status.PipeWire.SinkName, ternary(status.PipeWire.OutputSinkExists, "present", "missing"))
	fmt.Printf("Targets: %d\n", len(status.PipeWire.Targets))
	fmt.Printf("Devices: %d\n", len(status.Devices))
	fmt.Printf("Routes: %d\n", len(status.Routes))
	fmt.Printf("Groups: %d\n", len(status.Groups))

	if len(status.PipeWire.Targets) > 0 {
		fmt.Println()
		fmt.Println("PipeWire Targets")
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tSINK\tADDRESS\tVOLUME\tMUTED")
		for _, target := range status.PipeWire.Targets {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%t\n",
				target.Description,
				target.Name,
				emptyDash(target.Address),
				target.Volume,
				target.Muted,
			)
		}
		_ = tw.Flush()
	}

	if len(status.Routes) > 0 {
		fmt.Println()
		fmt.Println("Active Routes")
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "DEVICE\tSINK\tBACKEND\tVOLUME\tMUTED\tPAUSED\tSTATUS")
		for _, route := range status.Routes {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%t\t%t\t%s\n",
				route.DeviceName,
				route.PipeWireSinkName,
				route.TransportBackend,
				route.Volume,
				route.Muted,
				route.Paused,
				route.Status,
			)
		}
		_ = tw.Flush()
	}

	if len(status.Groups) > 0 {
		fmt.Println()
		fmt.Println("Groups")
		deviceNames := make(map[string]string, len(status.Devices))
		for _, device := range status.Devices {
			deviceNames[device.ID] = device.Name
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tDEVICES")
		for _, group := range status.Groups {
			names := make([]string, 0, len(group.Devices))
			for _, id := range group.Devices {
				if name, ok := deviceNames[id]; ok {
					names = append(names, name)
				} else {
					names = append(names, id)
				}
			}
			sort.Strings(names)
			fmt.Fprintf(tw, "%s\t%s\n", group.Name, strings.Join(names, ", "))
		}
		_ = tw.Flush()
	}
}

func pwSinkCell(device model.Device) string {
	if !device.HasPipeWireSink {
		return "-"
	}
	return fmt.Sprintf("%s (%d%%)", device.PipeWireSinkName, device.PipeWireSinkVolume)
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func requireArgs(args []string, count int, usage string) {
	if len(args) != count {
		exitErr(usage)
	}
}

func printUsage() {
	fmt.Println(`usage:
  tuxplay daemon
  tuxplay list
  tuxplay status
  tuxplay route [--add] <device>
  tuxplay unroute <device>
  tuxplay volume <device> <percent>
  tuxplay mute <device>
  tuxplay pause <device>
  tuxplay resume <device>
  tuxplay stop <device>
  tuxplay group create <name> <device> [device...]
  tuxplay group play <name>
  tuxplay group add <name> <device>
  tuxplay group remove <name> <device>`)
}

func ternary[T any](cond bool, left, right T) T {
	if cond {
		return left
	}
	return right
}

func exitErr(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
