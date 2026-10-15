package main

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	uciConfigPath   = "/etc/config/ztewifi"
	defaultIP       = "192.168.0.1"
	defaultPassword = ""
	defaultInterval = 5
)

// Config 是插件的运行配置。
// 取值优先级：命令行参数 > 环境变量 > UCI 配置 > 内置默认值。
type Config struct {
	IP       string
	Password string
	Interval int
	Enabled  bool
	Timeout  time.Duration
}

func defaultConfig() Config {
	return Config{
		IP:       defaultIP,
		Password: defaultPassword,
		Interval: defaultInterval,
		Enabled:  true,
		Timeout:  8 * time.Second,
	}
}

// LoadConfig 读取 UCI 配置并叠加环境变量。
func LoadConfig() Config {
	c := defaultConfig()

	if s := uciSection(uciConfigPath, "main"); s != nil {
		if v := s["ip"]; v != "" {
			c.IP = v
		}
		if v, ok := s["password"]; ok && v != "" {
			c.Password = v
		}
		if n, err := strconv.Atoi(s["interval"]); err == nil && n > 0 {
			c.Interval = n
		}
		if n, err := strconv.Atoi(s["timeout"]); err == nil && n > 0 {
			c.Timeout = time.Duration(n) * time.Second
		}
		if v, ok := s["enabled"]; ok {
			c.Enabled = v == "1" || v == "true" || v == "yes" || v == "on"
		}
	}

	if v := os.Getenv("ZTEWIFI_IP"); v != "" {
		c.IP = v
	}
	if v := os.Getenv("ZTEWIFI_PASSWORD"); v != "" {
		c.Password = v
	}

	return c
}

// uciSection 解析 UCI 配置文件，返回指定 section 的键值对。
// section 为空时返回第一个 section。文件不存在或解析失败返回 nil。
func uciSection(path, section string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	sections := map[string]map[string]string{}
	var order []string
	cur := ""

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "config":
			name := ""
			if len(fields) >= 3 {
				name = trimQuotes(fields[2])
			}
			if name == "" {
				name = fields[1]
			}
			if _, ok := sections[name]; !ok {
				sections[name] = map[string]string{}
				order = append(order, name)
			}
			cur = name
		case "option":
			if cur == "" || len(fields) < 3 {
				continue
			}
			sections[cur][fields[1]] = trimQuotes(strings.Join(fields[2:], " "))
		}
	}

	if section == "" {
		if len(order) == 0 {
			return nil
		}
		return sections[order[0]]
	}
	if s, ok := sections[section]; ok && len(s) > 0 {
		return s
	}
	// 退而求其次：只要有一个 section 就用它
	if len(order) > 0 {
		return sections[order[0]]
	}
	return nil
}

func trimQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
