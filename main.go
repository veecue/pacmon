package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/veecue/pacmon/udp"
)

const (
	port                 = 41234
	UDP_SERVER_COMMAND   = "GyDOS: PACMON: server:"
	UDP_DISCOVER_COMMAND = "GyDOS: PACMON: discover"
)

var (
	ErrStatus = errors.New("status")
	servers   = make(map[string]bool)
)

// Proxy proxies the incoming request from Pacman to another instance of pacmon
func Proxy(w http.ResponseWriter, r *http.Request, server string) error {
	resp, err := http.Get(server + r.URL.String())
	if err != nil {
		return err
	}
	if resp.StatusCode == 200 {
		for h := range resp.Header {
			w.Header().Add(h, resp.Header.Get(h))
		}
		_, err := io.Copy(w, resp.Body)
		return err
	} else {
		return ErrStatus
	}
}

func handler(w http.ResponseWriter, r *http.Request) {

	var packagePath = "/var/cache/pacman/pkg" + r.URL.String()

	fmt.Println(r.URL)

	ip := r.RemoteAddr[:strings.LastIndex(r.RemoteAddr, ":")]
	ip = strings.Trim(ip, "[]")
	if net.ParseIP(ip).IsLoopback() {
		// Redirect requests coming from local Pacman to other instances in the LAN
		for server := range servers {
			err := Proxy(w, r, server)
			if err == nil {
				return
			} else if err != ErrStatus {
				delete(servers, server)
			} else {
				fmt.Println(err)
			}
		}
		// No LAN mirror could provide the file, let pacman fall back to the rest of its mirrorlist
		http.NotFound(w, r)
	} else {
		if _, err := os.Stat(packagePath); os.IsNotExist(err) {
			http.NotFound(w, r)
		} else {
			http.ServeFile(w, r, packagePath)
		}
	}
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "mirrorlist" {
		mirrorlist()
	} else {
		fmt.Println("Started, local IP:", udp.GetLocalIP())
		go udp.ServeMulticastUDP(UDPHandler)
		time.Sleep(time.Second)
		go udp.SendMulicast(UDP_DISCOVER_COMMAND)
		time.Sleep(time.Second * 3)
		server()
	}
}

func server() {
	http.HandleFunc("/", handler)
	http.ListenAndServe(":"+strconv.FormatInt(port, 10), nil)
}

func UDPHandler(src *net.UDPAddr, n int, b []byte) {
	message := string(b[:n])

	if message == UDP_DISCOVER_COMMAND {
		udp.SendMulicast(UDP_SERVER_COMMAND + " " + udp.GetLocalIP() + ":" + strconv.FormatInt(port, 10))
	} else if strings.HasPrefix(message, UDP_SERVER_COMMAND) {
		if strings.Split(message[len(UDP_SERVER_COMMAND)+1:], ":")[0] != udp.GetLocalIP() {
			server := "http://" + message[len(UDP_SERVER_COMMAND)+1:]
			fmt.Println("Server found: ", server)
			servers[server] = true
		}
	}
}

func mirrorlist() {
	serverUrl := "http://localhost:" + strconv.FormatInt(port, 10)
	fmt.Println("Mirrorlist, local server:", serverUrl)

	checkLine := "Server = " + serverUrl //TODO: catch cases like "Server =http"...
	checkResult := false
	var updatedMirrorlist strings.Builder
	updatedMirrorlist.WriteString("# modified by pacmon on " + time.Now().Local().Format("2006-01-02") +
		"\n" + checkLine + "\n")

	file, err := os.Open("/etc/pacman.d/mirrorlist")
	if err != nil {
		fmt.Println("Couldn't read mirrorlist")
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		t := scanner.Text()
		if t == checkLine {
			checkResult = true
		}
		updatedMirrorlist.WriteString(t)
		updatedMirrorlist.WriteString("\n")
	}

	if err := scanner.Err(); err != nil {
		fmt.Println("Error:", err)
	}

	if !checkResult {
		err = ioutil.WriteFile("/etc/pacman.d/mirrorlist", []byte(updatedMirrorlist.String()), 0644)
		if err != nil {
			fmt.Println("Couldn't write to file: displaying contents:\n", updatedMirrorlist)
		}
	}
}
