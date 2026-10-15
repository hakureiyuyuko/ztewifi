package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ZTE 是随身 WiFi 网页后台(gofrom 接口)的客户端。
//
// 逆向自设备内置 js/service.js：
//   - 请求必须带 Referer + X-Requested-With，否则设备返回空
//   - 登录: GET cmd=LD 取挑战值, password = SHA256(SHA256(明文)+LD)
//   - 会话: LOGIN 响应下发 HttpOnly Cookie zsidn, 后续请求必须回传
type ZTE struct {
	IP     string
	passwd string
	cookie string
	ad     string
	client *http.Client
}

var reZSIDN = regexp.MustCompile(`(?i)\bzsidn=([0-9A-Fa-f]{8,})`)

func NewZTE(ip, password string, timeout time.Duration) *ZTE {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	return &ZTE{
		IP:     ip,
		passwd: password,
		client: &http.Client{
			Timeout: timeout,
			// 设备是 HTTP/1.0 语义，复用连接会导致 ECONNRESET
			Transport: &http.Transport{DisableKeepAlives: true},
		},
	}
}

func (z *ZTE) base() string { return "http://" + z.IP }

func (z *ZTE) do(method, path, body string) ([]byte, error) {
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, z.base()+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", z.base()+"/")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36")
	req.Header.Set("Cache-Control", "no-cache")
	if body != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if z.cookie != "" {
		req.Header.Set("Cookie", z.cookie)
	}
	req.Close = true

	resp, err := z.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	for _, sc := range resp.Header.Values("Set-Cookie") {
		if m := reZSIDN.FindStringSubmatch(sc); m != nil {
			z.cookie = "zsidn=" + m[1]
		}
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return data, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return data, nil
}

// GetCmds 批量读取命令，返回 命令名 -> 值 的映射。
func (z *ZTE) GetCmds(cmds []string) (map[string]string, error) {
	path := "/goform/goform_get_cmd_process?isTest=false&multi_data=1&cmd=" + url.QueryEscape(strings.Join(cmds, ","))
	data, err := z.do("GET", path, "")
	if err != nil {
		return nil, err
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		s := strings.TrimSpace(string(data))
		if s == "" {
			return nil, fmt.Errorf("empty response from device (not logged in or session expired)")
		}
		if len(s) > 200 {
			s = s[:200]
		}
		return nil, fmt.Errorf("cannot parse device response: %s", s)
	}

	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[k] = toStr(v)
	}
	return out, nil
}

// SetCmd 执行 goformId 动作。
func (z *ZTE) SetCmd(fields map[string]string) (map[string]string, error) {
	form := url.Values{"isTest": {"false"}}
	for k, v := range fields {
		form.Set(k, v)
	}

	// 设备前端 js/service.js 的 c()：除 LOGIN / SET_WEB_LANGUAGE 外，
	// 所有写操作的 body 都要带 AD 校验位，否则设备静默不生效。
	if gid := fields["goformId"]; gid != "" && gid != "LOGIN" && gid != "SET_WEB_LANGUAGE" {
		ad, err := z.accessID()
		if err != nil {
			return nil, err
		}
		form.Set("AD", ad)
	}

	data, err := z.do("POST", "/goform/goform_set_cmd_process", form.Encode())
	if err != nil {
		return nil, err
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("cannot parse device response: %.200s", string(data))
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[k] = toStr(v)
	}
	return out, nil
}

// accessID 计算写操作所需的 AD 校验位。
//	AD = SHA256( SHA256(rd_params0 + rd_params1) + RD )
//	其中 rd_params0 = wa_inner_version, rd_params1 = cr_version, RD 由 cmd=RD 读取
//	（对应设备前端 service.js 的 getLanguage() 与 util.js 的 cookWithRequest()）。
func (z *ZTE) accessID() (string, error) {
	if z.ad != "" {
		return z.ad, nil
	}

	m, err := z.GetCmds([]string{"cr_version", "wa_inner_version"})
	if err != nil {
		return "", err
	}
	rd, err := z.GetCmds([]string{"RD"})
	if err != nil {
		return "", err
	}
	if rd["RD"] == "" {
		return "", fmt.Errorf("cannot read RD challenge value")
	}

	z.ad = sha256Hex(sha256Hex(m["wa_inner_version"]+m["cr_version"]) + rd["RD"])
	return z.ad, nil
}

// Login 完成一次登录；成功后 z.cookie 即会话凭证。
func (z *ZTE) Login() error {
	m, err := z.GetCmds([]string{"LD"})
	if err != nil {
		return err
	}
	ld := m["LD"]
	if ld == "" {
		return fmt.Errorf("cannot read LD challenge (check the device address and network reachability)")
	}

	pwdField := sha256Hex(sha256Hex(z.passwd) + ld)
	res, err := z.SetCmd(map[string]string{"goformId": "LOGIN", "password": pwdField})
	if err != nil {
		return err
	}

	switch r := res["result"]; r {
	case "0", "4", "success", "":
		return nil
	case "3":
		return fmt.Errorf("login failed: wrong password")
	case "2":
		return fmt.Errorf("login failed: another client is already logged in (duplicateUser)")
	case "1":
		return fmt.Errorf("login failed")
	case "5":
		return fmt.Errorf("login failed: device currently unavailable")
	default:
		return fmt.Errorf("login failed: unexpected result=%s", r)
	}
}

// Logout 主动登出（尽量调用，失败忽略）。
func (z *ZTE) Logout() {
	_, _ = z.SetCmd(map[string]string{"goformId": "LOGOUT"})
}
func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return strings.ToUpper(hex.EncodeToString(h[:]))
}

func toStr(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "1"
		}
		return "0"
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}
