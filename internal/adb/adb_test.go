package adb

import (
	"bufio"
	"strings"
	"testing"
)

func TestParseDevices(t *testing.T) {
	out := `List of devices attached
R5CT1234ABC            device usb:1-1 product:PJF110 model:PJF110 device:OP5D0DL1 transport_id:3
192.168.1.20:5555      device product:panther model:Pixel_7 device:panther transport_id:4
adb-1A2B-xyz._adb-tls-connect._tcp device product:x model:y device:z transport_id:5
0123456789             unauthorized usb:1-2 transport_id:6
ZY22                   no permissions (missing udev rules? user is in the plugdev group); see [http://developer.android.com/tools/device.html] usb:1-3 transport_id:7
`
	list := ParseDevices(out)
	if len(list) != 5 {
		t.Fatalf("%d devices: %+v", len(list), list)
	}
	if e := list[0]; e.Serial != "R5CT1234ABC" || !e.Ready() || e.Model != "PJF110" || e.TransportID != "3" {
		t.Errorf("usb: %+v", e)
	}
	if e := list[1]; e.Model != "Pixel_7" || !IsWiFiSerial(e.Serial) {
		t.Errorf("tcp: %+v", e)
	}
	if !IsWiFiSerial(list[2].Serial) || IsWiFiSerial(list[0].Serial) {
		t.Error("mdns serial is Wi-Fi, USB serial is not")
	}
	if list[3].State != "unauthorized" || list[4].State != "no permissions" {
		t.Errorf("states %q %q", list[3].State, list[4].State)
	}
}

func TestHostProtocolFraming(t *testing.T) {
	var b strings.Builder
	sendRequest(&b, "host:track-devices-l")
	if b.String() != "0014host:track-devices-l" {
		t.Errorf("request %q", b.String())
	}
	r := bufio.NewReader(strings.NewReader("OKAY0005hello"))
	if err := readStatus(r); err != nil {
		t.Fatal(err)
	}
	if s, err := readHexPayload(r); err != nil || s != "hello" {
		t.Errorf("payload %q %v", s, err)
	}
	r = bufio.NewReader(strings.NewReader("FAIL0004nope"))
	if err := readStatus(r); err == nil || err.Error() != "nope" {
		t.Errorf("fail: %v", err)
	}
}

func TestInfo(t *testing.T) {
	props := ParseGetprop(`[ro.product.manufacturer]: [OnePlus]
[ro.product.brand]: [OnePlus]
[ro.product.model]: [PJF110]
[ro.vendor.oplus.market.name]: [OnePlus Ace 3V]
[ro.build.version.release]: [15]
[ro.serialno]: [abc123]
`)
	i := InfoFromProps("R5CT", props)
	if i.Name() != "OnePlus Ace 3V" || i.Subtitle() != "OnePlus · PJF110 · Android 15" || i.Identity() != "abc123" {
		t.Errorf("name %q subtitle %q identity %q", i.Name(), i.Subtitle(), i.Identity())
	}
	i = InfoFromProps("x", map[string]string{"ro.product.manufacturer": "samsung", "ro.product.model": "SM-S918B", "ro.build.version.release": "14"})
	if i.Name() != "Samsung SM-S918B" || i.Subtitle() != "Samsung · SM-S918B · Android 14" {
		t.Errorf("samsung: %q %q", i.Name(), i.Subtitle())
	}
	i = InfoFromProps("x", map[string]string{"ro.product.manufacturer": "Google", "ro.product.model": "Pixel 7"})
	if i.Name() != "Google Pixel 7" {
		t.Errorf("pixel: %q", i.Name())
	}
}

func TestParseRouteAndMDNS(t *testing.T) {
	route := `10.0.0.0/8 dev rmnet_data0 proto kernel scope link src 10.1.2.3
192.168.1.0/24 dev wlan0 proto kernel scope link src 192.168.1.42`
	if ip := ParseRouteAddress(route); ip != "192.168.1.42" {
		t.Errorf("ip %q", ip)
	}
	svcs := ParseMDNSServices(`List of discovered mdns services
airdroid-abc	_adb-tls-pairing._tcp	192.168.1.42:37099
adb-R5CT-xyz	_adb-tls-connect._tcp	192.168.1.42:41234
`)
	if len(svcs) != 2 || !svcs[0].Pairing() || svcs[1].Pairing() || svcs[1].Address != "192.168.1.42:41234" || svcs[0].Name != "airdroid-abc" {
		t.Errorf("services %+v", svcs)
	}
	q := QRPairing{Name: "airdroid-abc", Password: "secret"}
	if q.Payload() != "WIFI:T:ADB;S:airdroid-abc;P:secret;;" {
		t.Errorf("payload %q", q.Payload())
	}
}

func TestConnectResult(t *testing.T) {
	for out, ok := range map[string]bool{
		"connected to 192.168.1.42:5555":                               true,
		"already connected to 192.168.1.42:5555":                       true,
		"failed to connect to '192.168.1.42:5555': Connection refused": false,
		"cannot connect to 192.168.1.42:5555: No route to host (65)":   false,
	} {
		if err := connectResult(out); (err == nil) != ok {
			t.Errorf("%q: %v", out, err)
		}
	}
}
