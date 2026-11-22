package main

// The built-in "system" app: the network card, the devices, SMS and
// settings tiles, the network page and the settings page (settings.go). It uses the same view model as external apps, built
// here from the collector's Status instead of a command's output.

import (
	"fmt"
	"strconv"
	"strings"
)

const systemOrder = 10

func (s *Screen) systemView() View {
	st := s.st
	var v View

	speed := s.speeds(st.RxBps, st.TxBps)
	card := &Card{Title: s.tr("no_internet"), Subtitle: s.tr("no_route"), Icon: "cross", Color: "red", Open: "net"}
	switch st.WANKind {
	case "cell":
		card.Title = strings.Trim(st.Carrier+" · "+st.Tech, " ·")
		if st.Carrier == "" {
			card.Title = strings.TrimSpace(s.tr("mobile") + " " + st.Tech)
		}
		card.Subtitle, card.Icon, card.Color = speed, "signal-"+strconv.Itoa(st.Bars), "blue"
	case "wifi":
		card.Title = "Wi-Fi"
		if st.WANSSID != "" {
			card.Title = "Wi-Fi: " + st.WANSSID
		}
		card.Subtitle, card.Icon, card.Color = speed, "wifi", "blue"
	case "cable":
		card.Title, card.Subtitle, card.Icon, card.Color = s.tr("cable"), speed, "globe", "blue"
	case "usb":
		card.Title, card.Subtitle, card.Icon, card.Color = s.tr("usb_modem"), speed, "globe", "blue"
	case "other":
		card.Title, card.Subtitle, card.Icon, card.Color = s.trf("via", st.WANIface), speed, "globe", "blue"
	default:
		if st.CellAirplane {
			card.Title, card.Subtitle = s.tr("airplane"), s.tr("airplane_sub")
		}
		card.SubtitleColor = "red"
	}
	v.Card = card

	sms := "—"
	if st.SMSNew > 0 {
		sms = s.trf("sms_new", st.SMSNew)
	}
	v.Tiles = []Tile{
		{Label: s.tr("devices"), Value: strconv.Itoa(st.Clients), Icon: "devices", Open: "net"},
		{Label: s.tr("sms"), Value: sms, Icon: "sms", Open: "sms"},
		{Label: s.tr("settings"), Icon: "gear", Open: "settings"},
	}

	info := func(label, value string) Item { return Item{Type: "info", Label: label, Value: value} }
	p := &Page{Title: s.tr("net_title")}
	p.Items = append(p.Items, Item{Type: "section", Label: s.tr("sec_cell")}, s.airplaneItem())
	if st.CellOK {
		p.Items = append(p.Items,
			info(s.tr("carrier"), st.Carrier),
			info(s.tr("network"), st.Tech+" · SIM "+strconv.Itoa(st.Slot)))
		if st.RSRP != 0 {
			p.Items = append(p.Items, info(s.tr("signal"), fmt.Sprintf("RSRP %d · SINR %d", st.RSRP, st.SINR)))
		}
		if len(st.Bands) > 0 {
			p.Items = append(p.Items, info(s.tr("bands"), strings.Join(st.Bands, " + ")))
		}
	} else if st.CellAirplane {
		p.Items = append(p.Items, info(s.tr("network"), s.tr("airplane")))
	} else {
		p.Items = append(p.Items, info(s.tr("network"), "—"))
	}
	p.Items = append(p.Items, s.wifiItems()...)
	p.Items = append(p.Items, Item{Type: "section", Label: s.tr("sec_inet")})
	wan := st.WANIface
	if wan == "" {
		wan = s.tr("no_route")
	}
	p.Items = append(p.Items, info(s.tr("wan"), wan), info(s.tr("speed"), speed),
		info(s.tr("devices"), strconv.Itoa(st.Clients)))
	p.Items = append(p.Items, Item{Type: "section", Label: s.tr("sec_router")})
	if st.BatPct >= 0 {
		b := strconv.Itoa(st.BatPct) + "%"
		switch {
		case st.BatCharging:
			b += ", " + s.tr("charging")
		case st.BatPlugged:
			b += ", " + s.tr("plugged")
		}
		p.Items = append(p.Items, info(s.tr("battery"), b))
	}
	if st.TempC > 0 {
		p.Items = append(p.Items, info(s.tr("temp"), fmt.Sprintf("%.0f °C", st.TempC)))
	}
	p.Items = append(p.Items, info(s.tr("uptime"), s.duration(st.Uptime)))
	if st.Firmware != "" {
		p.Items = append(p.Items, info(s.tr("firmware"), "GL "+st.Firmware))
	}
	v.Pages = s.smsPages()
	v.Pages["net"], v.Pages["settings"], v.Pages["wifi"] = p, s.settingsPage(), s.wifiPage()
	return v
}

// speeds formats download and upload with one unit: "↓ 1,2  ↑ 0,1 Mbit/s".
func (s *Screen) speeds(rx, tx float64) string {
	dec := "."
	if s.lang == "ru" {
		dec = ","
	}
	num := func(v, unit float64) string {
		x := v / unit
		switch {
		case x >= 10 || x == 0:
			return fmt.Sprintf("%.0f", x)
		case x >= 0.05:
			return strings.Replace(fmt.Sprintf("%.1f", x), ".", dec, 1)
		}
		return "0"
	}
	if max(rx, tx) >= 1e6 {
		return "↓ " + num(rx, 1e6) + "  ↑ " + num(tx, 1e6) + " " + s.tr("mbit")
	}
	return "↓ " + num(rx, 1e3) + "  ↑ " + num(tx, 1e3) + " " + s.tr("kbit")
}
