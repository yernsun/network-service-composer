// workspace-dns supplies the Sandbox loopback fixture name in the disposable
// browser-test daemon. Docker continues to resolve actual service names itself.
package main

import (
	"log"
	"net"
	"os"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func main() {
	listen, upstream := ":53", "127.0.0.11:53"
	if len(os.Args) > 2 {
		listen, upstream = os.Args[1], os.Args[2]
	}
	conn, err := net.ListenPacket("udp4", listen)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	buffer := make([]byte, 4096)
	for {
		n, address, err := conn.ReadFrom(buffer)
		if err != nil {
			log.Fatal(err)
		}
		var query dnsmessage.Message
		if query.Unpack(buffer[:n]) != nil {
			continue
		}
		if len(query.Questions) == 1 && query.Questions[0].Name.String() != "localhost." {
			remote, err := net.DialTimeout("udp4", upstream, time.Second)
			if err == nil {
				_ = remote.SetDeadline(time.Now().Add(time.Second))
				_, _ = remote.Write(buffer[:n])
				if size, err := remote.Read(buffer); err == nil {
					_, _ = conn.WriteTo(buffer[:size], address)
				}
				_ = remote.Close()
			}
			continue
		}
		answer := dnsmessage.Message{Header: dnsmessage.Header{ID: query.ID, Response: true, Authoritative: true}, Questions: query.Questions}
		for _, question := range query.Questions {
			if question.Name.String() == "localhost." && question.Type == dnsmessage.TypeA {
				answer.Answers = append(answer.Answers, dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 1}, Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}})
			} else {
				answer.RCode = dnsmessage.RCodeNameError
			}
		}
		if packet, err := answer.Pack(); err == nil {
			_, _ = conn.WriteTo(packet, address)
		}
	}
}
