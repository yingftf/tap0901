package main

import (
	"encoding/hex"
	"fmt"
	"net"
	"os/exec"
	"src"
	"sync"
	"time"
)

func main() {

	/*************************************参数配置部分*****************************************************/

	/*************************************网卡配置部分*****************************************************/
	tun, err := src.OpenTun(net.IP([]byte{0, 0, 0, 0}), net.IP([]byte{0, 0, 0, 0}), net.IP([]byte{0, 0, 0, 0}))
	if err != nil {
		//panic(err)
		fmt.Printf("打开tap设备出错：%s", err)
	}

	tun.GetNetworkName(true)
	cmd := exec.Command("cmd.exe", "/c", "netsh interface ip set address \""+tun.GetNetworkName(true)+"\" static 10.10.10.99 255.255.255.0")
	err = cmd.Run()
	if err != nil {
		fmt.Println("设置ip地址失败!", err)
	} else {
		fmt.Println("设置ip地址成功!")
	}

	tun.Connect()
	time.Sleep(2 * time.Second)

	/*************************************进入监听处理部分*****************************************************/
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
	laddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:15645")
	raddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:12345")
	conn, err := net.DialUDP("udp", laddr, raddr)
	if err != nil {
		panic(err)
	}
	fmt.Println(tun.GetMTU(false))
	//fmt.Println(tun.Write([]byte{0x45, 0x00, 0x00, 0x23, 0x12, 0xa5, 0x00, 0x40, 0x11, 0xf3, 0x28, 0x7b, 0x7F, 0x00, 0x00, 0x01, 0x7F, 0x00, 0x00, 0x01, 0x1d, 0x63, 0x71, 0x00, 0x0f, 0x59, 0x17, 0x61, 0x62, 0x63, 0x64, 0x65, 0x66, 0x67}))
	tun.Write([]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01, 0x08, 0x06, 0x00, 0x01, 0x7F, 0x00, 0x00, 0x01, 0x1d, 0x63, 0x71, 0x00, 0x0f, 0x59, 0x17, 0x61, 0x62, 0x63, 0x64, 0x65, 0x66, 0x67})
	tun.Write([]byte{0x45, 0x00, 0x00, 0x23, 0x12, 0xa5, 0x00, 0x40, 0x11, 0xf3, 0x28, 0x7b, 0x7F, 0x00, 0x00, 0x01, 0x7F, 0x00, 0x00, 0x01, 0x1d, 0x63, 0x71, 0x00, 0x0f, 0x59, 0x17, 0x61, 0x62, 0x63, 0x64, 0x65, 0x66, 0x67})
	conn.Write([]byte("abcdefg2"))
	//conn.Write([]byte("abcdefg3"))
	//conn.Write([]byte("abcdefg4"))
	//tun.Write([]byte("abcdefg"))
	time.Sleep(300 * time.Second)

	/*************************************关闭服务部分*****************************************************/
	tun.SignalStop()
	wp.Wait()

}
