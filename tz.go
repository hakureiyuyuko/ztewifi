package main

import (
	"os"
	"regexp"
	"strings"
	"time"
)

// OpenWrt 把时区放在 /etc/TZ（POSIX TZ 串，如 "CST-8"），
// 而 Go 只认 $TZ 或 /etc/localtime，且不解析 POSIX 串，
// 所以这里手动解析，否则输出时间会变成 UTC。
var rePosixTZ = regexp.MustCompile(`^([A-Za-z]{2,}|<[^>]+>)([+-]?)(\d{1,2})(?::(\d{2})(?::(\d{2}))?)?`)

func init() {
	if os.Getenv("TZ") != "" {
		return
	}
	b, err := os.ReadFile("/etc/TZ")
	if err != nil {
		return
	}
	loc := parsePosixTZ(strings.TrimSpace(string(b)))
	if loc != nil {
		time.Local = loc
	}
}

// parsePosixTZ 解析 POSIX TZ 串的“标准时间”部分，返回固定时区。
// 形如 CST-8 => UTC+8；EST5 => UTC-5；（夏令时部分忽略，中国无夏令时）
func parsePosixTZ(tz string) *time.Location {
	m := rePosixTZ.FindStringSubmatch(tz)
	if m == nil {
		return nil
	}
	name := strings.Trim(m[1], "<>")
	h := atoi(m[3])
	mi := atoi(m[4])
	se := atoi(m[5])

	sign := 1
	if m[2] == "-" {
		sign = -1
	}
	// POSIX 的 offset 表示“本地时间 + offset = UTC”（西偏），所以东偏 = -offset
	// 例：CST-8 => offset=-28800 => 东偏 +28800（UTC+8）
	offset := sign * (h*3600 + mi*60 + se)
	east := -offset

	if name == "" {
		name = "Local"
	}
	return time.FixedZone(name, east)
}
