package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const version = "1.0.0"

const sessionFile = "/tmp/ztewifi.session"
const sessionTTL = 300 // 秒，会话缓存有效期，避免每次轮询都重新登录

// 需要向设备查询的命令列表（一次请求批量取回）
var statusCmds = []string{
	"network_type", "network_signalbar",
	"Z5g_rsrp", "Z5g_rsrq", "Z5g_SINR", "Z5g_snr",
	"network_lte_rsrp", "network_lte_rssi", "lte_rssi",
	"network_rssi", "network_rscp", "network_ecio",
	"wan_active_band", "wan_active_channel", "bandwidth",
	"network_cell_id", "network_pci",
	"network_lte_ca_pcell_band", "network_lte_ca_scell_band", "wan_lte_ca",
	"network_provider_fullname", "network_rmcc", "network_rmnc", "network_simcard_roam",
	"sim_iccid", "sim_imsi", "sim_msisdn", "imei",
	"device_market_name", "model_name", "wa_inner_version",
	"ppp_status", "mc_modem_main_state", "opms_wan_mode",
	"flux_realtime_tx_thrpt", "flux_realtime_rx_thrpt",
	"battery_value", "battery_vol_percent", "battery_charging", "battery_exist",
	"wifi_onoff_state", "wifi_chip1_ssid1_ssid", "wifi_chip2_ssid1_ssid",
	"wifi_chip1_ssid1_switch_onoff", "wifi_chip2_ssid1_switch_onoff",
	"wan_ipaddr", "lan_ipaddr",
}

// ---------------- 输出结构 ----------------

type Status struct {
	OK      bool        `json:"ok"`
	Error   string      `json:"error,omitempty"`
	IP      string      `json:"ip"`
	Time    string      `json:"time"`
	TS      int64       `json:"ts"`
	Network NetworkInfo `json:"network"`
	Signal  SignalInfo  `json:"signal"`
	Device  DeviceInfo  `json:"device"`
	Battery BatteryInfo `json:"battery"`
	WiFi    WiFiInfo    `json:"wifi"`
	Traffic TrafficInfo `json:"traffic"`
}

type NetworkInfo struct {
	Type       string `json:"type"`
	Band       string `json:"band"`
	Channel    string `json:"channel"`
	CellID     string `json:"cell_id"`
	Provider   string `json:"provider"`
	MCC        string `json:"mcc"`
	MNC        string `json:"mnc"`
	Roam       string `json:"roam"`
	PPPStatus  string `json:"ppp_status"`
	WANIP      string `json:"wan_ip"`
	LANIP      string `json:"lan_ip"`
	ModemState string `json:"modem_state"`
	WANMode    string `json:"wan_mode"`
}

type SignalInfo struct {
	Bars    int     `json:"bars"`
	Level   string  `json:"level"`
	RSRP    int     `json:"rsrp"`
	RSRQ    int     `json:"rsrq"`
	SINR    float64 `json:"sinr"`
	RSSI    int     `json:"rssi"`
	LTERSRP int     `json:"lte_rsrp"`
	LTERSSI int     `json:"lte_rssi"`
}

type DeviceInfo struct {
	Model    string `json:"model"`
	Market   string `json:"market"`
	Firmware string `json:"firmware"`
	IMEI     string `json:"imei"`
	ICCID    string `json:"iccid"`
	IMSI     string `json:"imsi"`
	MSISDN   string `json:"msisdn"`
}

type BatteryInfo struct {
	Percent  int  `json:"percent"`
	Charging bool `json:"charging"`
	Present  bool `json:"present"`
}

type WiFiInfo struct {
	Enabled bool   `json:"enabled"`
	SSID    string `json:"ssid"`
	Chip1   bool   `json:"chip1"`
	Chip2   bool   `json:"chip2"`
}

type TrafficInfo struct {
	TX int64 `json:"tx"`
	RX int64 `json:"rx"`
}

// ---------------- 命令实现 ----------------

func fetchStatus(cfg Config) *Status {
	now := time.Now()
	st := &Status{
		IP:   cfg.IP,
		Time: now.Format("2006-01-02 15:04:05"),
		TS:   now.Unix(),
	}
	if !cfg.Enabled {
		st.Error = "plugin disabled (enable it under Services -> ZTE Portable WiFi -> Settings)"
		return st
	}

	z, err := newSession(cfg)
	if err != nil {
		st.Error = err.Error()
		return st
	}

	m, err := z.GetCmds(statusCmds)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	if m["network_type"] == "" {
		st.Error = "logged in but no data returned (session may have been taken over by another client)"
		return st
	}

	buildStatus(st, m)
	st.OK = true
	return st
}

func buildStatus(st *Status, m map[string]string) {
	g := func(k string) string { return m[k] }

	st.Network = NetworkInfo{
		Type:       g("network_type"),
		Band:       g("wan_active_band"),
		Channel:    g("wan_active_channel"),
		CellID:     g("network_cell_id"),
		Provider:   g("network_provider_fullname"),
		MCC:        g("network_rmcc"),
		MNC:        g("network_rmnc"),
		Roam:       g("network_simcard_roam"),
		PPPStatus:  g("ppp_status"),
		WANIP:      g("wan_ipaddr"),
		LANIP:      g("lan_ipaddr"),
		ModemState: g("mc_modem_main_state"),
		WANMode:    g("opms_wan_mode"),
	}

	is5G := strings.Contains(strings.ToUpper(st.Network.Type), "NR") || strings.Contains(strings.ToUpper(st.Network.Type), "5G")
	rsrp := atoi(g("Z5g_rsrp"))
	rsrq := atoi(g("Z5g_rsrq"))
	sinr := atof(g("Z5g_SINR"))
	if sinr == 0 {
		sinr = atof(g("Z5g_snr"))
	}
	if !is5G {
		rsrp = atoi(g("network_lte_rsrp"))
		rsrq = atoi(g("Z5g_rsrq"))
	}
	st.Signal = SignalInfo{
		Bars:    atoi(g("network_signalbar")),
		Level:   signalLevel(rsrp),
		RSRP:    rsrp,
		RSRQ:    rsrq,
		SINR:    sinr,
		RSSI:    atoi(firstNonEmpty(g("network_rssi"), g("network_lte_rssi"), g("lte_rssi"))),
		LTERSRP: atoi(g("network_lte_rsrp")),
		LTERSSI: atoi(firstNonEmpty(g("network_lte_rssi"), g("lte_rssi"))),
	}

	st.Device = DeviceInfo{
		Model:    g("model_name"),
		Market:   g("device_market_name"),
		Firmware: g("wa_inner_version"),
		IMEI:     g("imei"),
		ICCID:    g("sim_iccid"),
		IMSI:     g("sim_imsi"),
		MSISDN:   g("sim_msisdn"),
	}

	charge := g("battery_charging")
	st.Battery = BatteryInfo{
		Percent:  atoi(firstNonEmpty(g("battery_value"), g("battery_vol_percent"))),
		Charging: charge == "1" || charge == "3",
		Present:  g("battery_exist") == "1" || charge == "2",
	}

	st.WiFi = WiFiInfo{
		Enabled: g("wifi_onoff_state") == "1",
		SSID:    firstNonEmpty(g("wifi_chip1_ssid1_ssid"), g("wifi_chip2_ssid1_ssid")),
		Chip1:   g("wifi_chip1_ssid1_switch_onoff") == "1",
		Chip2:   g("wifi_chip2_ssid1_switch_onoff") == "1",
	}

	st.Traffic = TrafficInfo{
		TX: atoi64(g("flux_realtime_tx_thrpt")),
		RX: atoi64(g("flux_realtime_rx_thrpt")),
	}
}

func signalLevel(rsrp int) string {
	switch {
	case rsrp == 0:
		return ""
	case rsrp >= -80:
		return "excellent"
	case rsrp >= -90:
		return "good"
	case rsrp >= -100:
		return "fair"
	case rsrp >= -110:
		return "weak"
	default:
		return "poor"
	}
}

// ---------------- 会话 ----------------

// newSession 返回一个已登录的客户端：优先复用 /tmp 里的会话缓存，失效则重新登录。
func newSession(cfg Config) (*ZTE, error) {
	z := NewZTE(cfg.IP, cfg.Password, cfg.Timeout)

	if ck := loadSession(cfg.IP); ck != "" {
		z.cookie = ck
		if m, err := z.GetCmds([]string{"network_type"}); err == nil && m["network_type"] != "" {
			return z, nil
		}
	}

	if err := z.Login(); err != nil {
		return nil, err
	}
	saveSession(cfg.IP, z.cookie)
	return z, nil
}

// ---------------- 设备 WiFi 开关 ----------------
//
// 逆向自设备前端 js/wifi/wifi_main.js + js/service.js：
//
//	关闭：goformId=switchWiFiModule, SwitchOption=0
//	开启：goformId=switchWiFiChip, ChipEnum=<chip1_2|chip1|chip2>, GuestEnable=0
//
// ChipEnum 按当前各芯片的 AP 开关状态推导（同 util.js 的 handleWifiChipStatus）。
var wifiStateCmds = []string{
	"wifi_onoff_state",
	"wifi_chip1_ssid1_switch_onoff",
	"wifi_chip2_ssid1_switch_onoff",
}

func chipEnumWith(m map[string]string) string {
	c1 := m["wifi_chip1_ssid1_switch_onoff"] == "1"
	c2 := m["wifi_chip2_ssid1_switch_onoff"] == "1"
	switch {
	case c1 && c2:
		return "chip1_2"
	case c1:
		return "chip1"
	case c2:
		return "chip2"
	default:
		return "chip1_2"
	}
}

// wifiControl 处理 `ztewifi wifi on|off|status`。
func wifiControl(cfg Config, action string) map[string]interface{} {
	res := map[string]interface{}{"ok": false, "ip": cfg.IP, "action": action}

	if !cfg.Enabled {
		res["message"] = "plugin disabled (enable it under Services -> ZTE Portable WiFi -> Settings)"
		return res
	}

	z, err := newSession(cfg)
	if err != nil {
		res["message"] = err.Error()
		return res
	}

	cur, err := z.GetCmds(wifiStateCmds)
	if err != nil {
		res["message"] = err.Error()
		return res
	}

	on := cur["wifi_onoff_state"] == "1"
	res["on"] = on

	if action == "" || action == "status" {
		res["ok"] = true
		res["chip"] = chipEnumWith(cur)
		return res
	}

	var want bool
	switch action {
	case "on", "1", "enable", "open", "start":
		want = true
	case "off", "0", "disable", "close", "stop":
		want = false
	default:
		res["message"] = "unknown argument " + action + " (expected: on / off / status)"
		return res
	}

	if on == want {
		res["ok"] = true
		res["message"] = "device WiFi is already " + onOff(want)
		return res
	}

	if want {
		chip := chipEnumWith(cur)
		res["chip"] = chip
		resp, err := z.SetCmd(map[string]string{
			"goformId":    "switchWiFiChip",
			"ChipEnum":    chip,
			"GuestEnable": "0",
		})
		res["response"] = resp
		if err != nil {
			res["message"] = err.Error()
			return res
		}
	} else {
		resp, err := z.SetCmd(map[string]string{
			"goformId":     "switchWiFiModule",
			"SwitchOption": "0",
		})
		res["response"] = resp
		if err != nil {
			res["message"] = err.Error()
			return res
		}
	}

	// 设备切换需要几秒，轮询等待生效
	var last string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(1500 * time.Millisecond)
		m, err := z.GetCmds([]string{"wifi_onoff_state"})
		if err != nil {
			last = err.Error()
			continue
		}
		last = m["wifi_onoff_state"]
		if (last == "1") == want {
			res["ok"] = true
			res["on"] = want
			res["message"] = "device WiFi turned " + onOff(want)
			return res
		}
	}

	res["on"] = last == "1"
	res["message"] = "command sent but device state did not become " + onOff(want) +
		" within the timeout (wifi_onoff_state=" + last + ")"
	return res
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

type sessionRec struct {
	IP     string `json:"ip"`
	Cookie string `json:"cookie"`
	TS     int64  `json:"ts"`
}

func loadSession(ip string) string {
	data, err := os.ReadFile(sessionFile)
	if err != nil {
		return ""
	}
	var r sessionRec
	if err := json.Unmarshal(data, &r); err != nil {
		return ""
	}
	if r.IP != ip || r.Cookie == "" || time.Now().Unix()-r.TS > sessionTTL {
		return ""
	}
	return r.Cookie
}

func saveSession(ip, cookie string) {
	if cookie == "" {
		return
	}
	data, _ := json.Marshal(sessionRec{IP: ip, Cookie: cookie, TS: time.Now().Unix()})
	_ = os.WriteFile(sessionFile, data, 0600)
}

// ---------------- 工具 ----------------

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func atoi(s string) int { n, _ := strconv.Atoi(strings.TrimSpace(s)); return n }
func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}
func atof(s string) float64 { f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64); return f }

// parseArgs 解析参数：支持 --k v、--k=v、-k v；无值开关自动置 "true"。
// 第二个返回值是位置参数（不以 - 开头的），例如 `ztewifi wifi on` 里的 "on"。
func parseArgs(args []string) (map[string]string, []string) {
	out := map[string]string{}
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			pos = append(pos, a)
			continue
		}
		a = strings.TrimLeft(a, "-")
		if eq := strings.Index(a, "="); eq >= 0 {
			out[a[:eq]] = a[eq+1:]
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			out[a] = args[i+1]
			i++
		} else {
			out[a] = "true"
		}
	}
	return out, pos
}

func uciSet(key, val string) error {
	if err := exec.Command("/sbin/uci", "-q", "get", "ztewifi.main").Run(); err != nil {
		if err := exec.Command("/sbin/uci", "set", "ztewifi.main=ztewifi").Run(); err != nil {
			return fmt.Errorf("failed to create uci section: %w", err)
		}
	}
	if err := exec.Command("/sbin/uci", "set", fmt.Sprintf("ztewifi.main.%s=%s", key, val)).Run(); err != nil {
		return fmt.Errorf("failed to set %s: %w", key, err)
	}
	return nil
}

func uciCommit() error {
	return exec.Command("/sbin/uci", "commit", "ztewifi").Run()
}
