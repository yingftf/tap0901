package src

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"
	"unsafe"

	//	"os"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (

	//TAP 网卡注册项 及 文件系统前缀
	TAPWIN32_MAX_REG_SIZE   = 256
	TUNTAP_COMPONENT_ID     = "tap0901"
	ADAPTER_KEY             = `SYSTEM\CurrentControlSet\Control\Class\{4D36E972-E325-11CE-BFC1-08002BE10318}`
	NETWORK_CONNECTIONS_KEY = `SYSTEM\CurrentControlSet\Control\Network\{4D36E972-E325-11CE-BFC1-08002BE10318}`
	USERMODEDEVICEDIR       = `\\.\Global\`
	SYSDEVICEDIR            = `\Device\`
	USERDEVICEDIR           = `\DosDevices\Global`
	TAP_WIN_SUFFIX          = ".tap"

	//TAP IOCTLs
	TAP_WIN_IOCTL_GET_MAC               = 1
	TAP_WIN_IOCTL_GET_VERSION           = 2
	TAP_WIN_IOCTL_GET_MTU               = 3
	TAP_WIN_IOCTL_GET_INFO              = 4
	TAP_WIN_IOCTL_CONFIG_POINT_TO_POINT = 5
	TAP_WIN_IOCTL_SET_MEDIA_STATUS      = 6
	TAP_WIN_IOCTL_CONFIG_DHCP_MASQ      = 7
	TAP_WIN_IOCTL_GET_LOG_LINE          = 8
	TAP_WIN_IOCTL_CONFIG_DHCP_SET_OPT   = 9
	TAP_WIN_IOCTL_CONFIG_TUN            = 10
	//
	FILE_ANY_ACCESS = 0
	METHOD_BUFFERED = 0
	//
	IO_BUFFER_NUM = 1024
	MAX_PROCS     = 1024
)

//
var (
	componentId string
)

type Tun struct {
	ID               string
	MTU              uint32
	DevicePath       string
	FD               syscall.Handle
	NetworkName      string
	received         chan []byte
	toSend           chan []byte
	readReqs         chan event
	reusedOverlapped syscall.Overlapped // reuse for write
	reusedEvent      syscall.Handle
	listening        bool
	readHandler      func(tun *Tun, data []byte)
	closeWorker      chan bool
	procs            int
}

// 10 OpenTun 打开tap0901设备并设置 TAP_WIN_IOCTL_CONFIG_TUN
// Params: addr -> the localIPAddr
//         network -> remoteNetwork
//         mask -> remoteNetmask
// 该函数用于为后续的操作配置网络
// tun将处理本地ip和远程网络之间的传输
func OpenTun(addr, network, mask net.IP) (*Tun, error) { // wintap.c 225 需改造
	id, err := getTuntapComponentId() //读取并返回TUNTAP设备注册表中 "NetCfgInstanceId" 键值
	if err != nil {
		return nil, err
	}

	reusedE, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return nil, err
	}
	tun := &Tun{
		ID:          id,
		DevicePath:  fmt.Sprintf(USERMODEDEVICEDIR+"%s"+TAP_WIN_SUFFIX, id), //设备系统路径
		received:    make(chan []byte, IO_BUFFER_NUM),
		toSend:      make(chan []byte, IO_BUFFER_NUM),
		readReqs:    make(chan event, IO_BUFFER_NUM),
		closeWorker: make(chan bool, MAX_PROCS),
		procs:       0,
		reusedEvent: syscall.Handle(reusedE),
	}
	tun.reusedOverlapped.HEvent = tun.reusedEvent

	fName := syscall.StringToUTF16(tun.DevicePath)
	tun.FD, err = syscall.CreateFile(
		&fName[0],
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0, //syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_SYSTEM|syscall.FILE_FLAG_OVERLAPPED,
		0,
	)
	if err != nil {
		return nil, err
	}

	var returnLen uint32
	configTunParam := append(addr.To4(), network.To4()...)
	configTunParam = append(configTunParam, mask.To4()...)
	err = syscall.DeviceIoControl(tun.FD, tap_ioctl(TAP_WIN_IOCTL_CONFIG_TUN), //TUN网卡配置项
		&configTunParam[0], uint32(len(configTunParam)),
		&configTunParam[0], uint32(len(configTunParam)), // I think here can be nil
		&returnLen, nil)
	if err != nil {
		return nil, err
	}

	return tun, nil
}

// 03 获取TUN设备MTU TAP_WIN_IOCTL_GET_MTU
func (tun *Tun) GetMTU(refresh bool) uint32 {
	if !refresh && tun.MTU != 0 {
		return tun.MTU
	}

	var returnLen uint32
	var umtu = make([]byte, 4)
	err := syscall.DeviceIoControl(tun.FD, tap_ioctl(TAP_WIN_IOCTL_GET_MTU),
		&umtu[0], uint32(len(umtu)),
		&umtu[0], uint32(len(umtu)),
		&returnLen, nil)
	if err != nil {
		return 0
	}
	tun.MTU = binary.LittleEndian.Uint32(umtu)

	return tun.MTU
}

// 06 将驱动程序媒体状态设置为“已连接” TAP_WIN_IOCTL_SET_MEDIA_STATUS
func (tun *Tun) Connect() error {
	var returnLen uint32
	inBuffer := []byte("\x01\x00\x00\x00") // only means TRUE
	err := syscall.DeviceIoControl(
		tun.FD, tap_ioctl(TAP_WIN_IOCTL_SET_MEDIA_STATUS),
		&inBuffer[0], uint32(len(inBuffer)),
		&inBuffer[0], uint32(len(inBuffer)),
		&returnLen, nil)
	return err
}

// 07 设置DHCP连接 TAP_WIN_IOCTL_CONFIG_DHCP_MASQ
func (tun *Tun) SetDHCPMasq(dhcpAddr, dhcpMask, serverIP, leaseTime net.IP) error {
	var returnLen uint32
	configTunParam := append(dhcpAddr.To4(), dhcpMask.To4()...)
	configTunParam = append(configTunParam, serverIP.To4()...)
	configTunParam = append(configTunParam, leaseTime.To4()...)
	err := syscall.DeviceIoControl(tun.FD, tap_ioctl(TAP_WIN_IOCTL_CONFIG_DHCP_MASQ),
		&configTunParam[0], uint32(len(configTunParam)),
		&configTunParam[0], uint32(len(configTunParam)), // I think here can be nil
		&returnLen, nil)
	return err
}

// 获取虚拟设备的名称 例如：“本地连接4”
func (tun *Tun) GetNetworkName(refresh bool) string {
	if !refresh && tun.NetworkName != "" {
		return tun.NetworkName
	}
	keyName := `SYSTEM\CurrentControlSet\Control\Network\{4D36E972-E325-11CE-BFC1-08002BE10318}\` +
		tun.ID + `\Connection`
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, keyName, registry.ALL_ACCESS)
	if err != nil {
		return ""
	}
	szname, _, err := k.GetStringValue("Name")
	if err != nil {
		return ""
	}
	k.Close()
	tun.NetworkName = szname
	return szname
}

//返回tap ioctl 控制编码 OpenTun->tap_ioctl->tap_control_code
func ctl_code(device_type, function, method, access uint32) uint32 {
	return (device_type << 16) | (access << 14) | (function << 2) | method
}

//返回tap ioctl 控制编码 OpenTun->tap_ioctl
func tap_control_code(request, method uint32) uint32 {
	return ctl_code(34, request, method, FILE_ANY_ACCESS)
}

// 返回tap ioctl 控制编码 OpenTun->
func tap_ioctl(cmd uint32) uint32 {
	return tap_control_code(cmd, METHOD_BUFFERED)
}

//遍历查找注册表键值 OpenTun->getTuntapComponentId()->getTuntapComponentId()->
func matchKey(zones registry.Key, kName string, componentId string) (string, error) {
	k, err := registry.OpenKey(zones, kName, registry.READ)
	if err != nil {
		return "", err
	}
	defer k.Close()

	cId, _, err := k.GetStringValue("ComponentId") //读取键值ComponentId
	if cId == componentId {                        //判断是否是"tap0901"
		netCfgInstanceId, _, err := k.GetStringValue("NetCfgInstanceId") //读取键值NetCfgInstanceId
		if err != nil {
			return "", err
		}
		return netCfgInstanceId, nil //返回NetCfgInstanceId的值

	}
	return "", fmt.Errorf("ComponentId != componentId")
}

//获取Tuntap设备ID OpenTun->
func getTuntapComponentId() (string, error) {
	if componentId != "" {
		return componentId, nil //如果componentId存在，直接返回该值
	}

	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		ADAPTER_KEY, //注册表值 `SYSTEM\CurrentControlSet\Control\Class\{4D36E972-E325-11CE-BFC1-08002BE10318}`
		registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()

	names, err := k.ReadSubKeyNames(-1) //检查读取的键名称，没有报错
	if err != nil {
		return "", err
	}

	for _, name := range names {
		n, _ := matchKey(k, name, TUNTAP_COMPONENT_ID) //遍历查找注册表键值
		if n != "" {
			componentId = n
			return n, nil
		}
	}
	return "", fmt.Errorf("Not Found")
}

/******************************************IO.go*******************************************************/

const IO_CYCLE_TIME_OUT = 100

var (
	kernel32              syscall.Handle
	waitForMutipleObjects uintptr
)

func init() {
	var err error
	kernel32, err = syscall.LoadLibrary("kernel32")
	if err != nil {
		panic(err)
	}
	waitForMutipleObjects, err = syscall.GetProcAddress(kernel32, "WaitForMultipleObjects")
	if err != nil {
		panic(err)
	}
}

func WaitForMultipleObjects(nCount uint32, handles *syscall.Handle, waitAll bool, milliseconds uint32) (uint32, error) {
	var dwWaitAll uintptr
	if waitAll {
		dwWaitAll = 1
	} else {
		dwWaitAll = 0
	}
	ret, _, err := syscall.Syscall6(waitForMutipleObjects, 4, uintptr(nCount), uintptr(unsafe.Pointer(handles)),
		dwWaitAll, uintptr(milliseconds), 0, 0)
	return uint32(ret), err
}

type event struct {
	hev  syscall.Handle
	buff []byte
}

func (tun *Tun) SetReadHandler(handler func(tun *Tun, data []byte)) error {
	if tun.listening {
		return errors.New("tun already listenning")
	}
	tun.readHandler = handler
	return nil
}

func (tun *Tun) Write(data []byte) error {
	if !tun.listening {
		return errors.New("tun is not listenning")
	}
	var l uint32
	return syscall.WriteFile(tun.FD, data, &l, &tun.reusedOverlapped)
}

//读取数据
func (tun *Tun) postReadRequest() error {
	hevent, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return err
	}
	ev := event{
		hev:  (syscall.Handle)(hevent),
		buff: make([]byte, tun.GetMTU(false)),
	}
	tun.readReqs <- ev

	overlapped := syscall.Overlapped{}
	overlapped.HEvent = ev.hev
	var l uint32
	return syscall.ReadFile(tun.FD, ev.buff, &l, &overlapped)
}

//开始工作
func (tun *Tun) Worker() {
	for tun.listening {
		if err := tun.postReadRequest(); err != nil {
		}

		select {
		case data := <-tun.received:
			tun.readHandler(tun, data)
		case <-tun.closeWorker:
			break
		}
	}
}

//tun停止服务
func (tun *Tun) SignalStop() error {
	if !tun.listening {
		return errors.New("tun is not listenning")
	}
	tun.listening = false
	for i := 0; i < tun.procs; i++ {
		tun.closeWorker <- true
	}
	return nil
}

func (tun *Tun) Listen(procs int) error {
	tun.listening = true
	tun.procs = procs
	var wp sync.WaitGroup

	revents := make([]syscall.Handle, 0)
	evs := make([]event, 0)

	start := sync.WaitGroup{}
	for i := 0; i < procs; i++ {
		start.Add(1)
		go func() {
			start.Done()
			tun.Worker()
			wp.Done()
		}()
		wp.Add(1)
	}
	defer wp.Wait()
	start.Wait()

	for tun.listening {
	SELECT:
		for {
			select {
			case ev := <-tun.readReqs:
				revents = append(revents, ev.hev)
				evs = append(evs, ev)
			default:
				if len(revents) != 0 {
					break SELECT
				}
			}
		}

		var e uint32

		e, _ = WaitForMultipleObjects(uint32(len(revents)), &revents[0], false, IO_CYCLE_TIME_OUT)

		switch e {
		case syscall.WAIT_FAILED:
			return errors.New("wait failed")
		case syscall.WAIT_TIMEOUT:
			continue
		default:
			nIndex := e - syscall.WAIT_OBJECT_0
			tun.received <- evs[nIndex].buff

			evs = append(evs[0:nIndex], evs[nIndex+1:]...)
			revents = append(revents[0:nIndex], revents[nIndex+1:]...)
		}
	}
	return nil
}

/**************************************queue.go**********************************************************/

const QUEUE_SIZE = 1024

type queue struct {
	data  [QUEUE_SIZE]interface{}
	read  int
	write int
}

func (q *queue) pop(t *interface{}) bool {
	if q.data[q.read] == nil {
		return false
	}
	*t = q.data[q.read]
	q.data[q.read] = nil
	if q.read+1 >= QUEUE_SIZE {
		q.read += 1 - QUEUE_SIZE
	} else {
		q.read++
	}
	return true
}

func (q *queue) push(t interface{}) bool {
	if q.data[q.write] == nil {
		// WMB
		q.data[q.write] = t
		if q.write+1 >= QUEUE_SIZE {
			q.write += 1 - QUEUE_SIZE
		} else {
			q.write++
		}
		return true
	}
	return false
}
