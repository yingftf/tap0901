package main

import (
	"encoding/hex"
	"fmt"
	"net"
	"src"
	"sync"
	"time"
)

func main() {
	tun, err := src.OpenTun([]byte{123, 123, 123, 123}, []byte{0, 0, 0, 0}, []byte{0, 0, 0, 0})
	if err != nil {
		panic(err)
	}
	tun.Connect()
	time.Sleep(2 * time.Second)

	tun.SetReadHandler(func(tun *src.Tun, data []byte) {
		fmt.Println(hex.EncodeToString(data))
	})
	wp := sync.WaitGroup{}
	wp.Add(1)
	go func() {
		tun.Listen(1)
		wp.Done()
	}()
	time.Sleep(2 * time.Second)
	laddr, _ := net.ResolveUDPAddr("udp4", "127.0.0.1:15645")
	raddr, _ := net.ResolveUDPAddr("udp4", "127.0.0.1:12345")
	conn, err := net.DialUDP("udp4", laddr, raddr)
	if err != nil {
		panic(err)
	}
	//fmt.Println(tun.GetMTU(false))
	//fmt.Println(tun.Write([]byte{0x45, 0x00, 0x00, 0x23, 0x12, 0xa5, 0x00, 0x40, 0x11, 0xf3, 0x28, 0x7b, 0x02, 0x03, 0x04, 0x7b, 0x7b, 0x7b, 0x7b, 0x3d, 0x1d, 0x63, 0x71, 0x00, 0x0f, 0x59, 0x17, 0x61, 0x62, 0x63, 0x64, 0x65, 0x66, 0x67}))
	conn.Write([]byte("abcdefg2"))
	conn.Write([]byte("abcdefg3"))
	conn.Write([]byte("abcdefg4"))
	//tun.Write([]byte("abcdefg"))
	time.Sleep(10 * time.Second)
	tun.SignalStop()
	wp.Wait()

}
