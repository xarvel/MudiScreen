package main

// Strings of the screen itself. Apps bring their own and get the language in
// MUDI_SCREEN_LANG.

import (
	"fmt"
	"time"
)

var languages = []string{"en", "ru"}

var dict = map[string]map[string]string{
	"en": {
		"tap_to_open":         "Tap to open",
		"enter_code":          "Enter passcode",
		"wrong_code":          "Wrong passcode",
		"wait_n":              "Too many attempts, wait %d s",
		"cancel":              "Cancel",
		"ok":                  "OK",
		"open":                "Open",
		"yes":                 "Yes",
		"power":               "Power",
		"power_off":           "Power off",
		"reboot":              "Restart",
		"confirm_off":         "Power off the router?",
		"confirm_rb":          "Restart the router?",
		"power_note":          "The internet will be gone",
		"bye_off":             "Powering off…",
		"bye_reboot":          "Restarting…",
		"stock":               "Stock screen…",
		"charger_on":          "Charger connected",
		"charger_off":         "Charger disconnected",
		"bat_low_t":           "Low battery",
		"bat_low":             "Connect the charger",
		"bat_crit_t":          "Battery almost empty",
		"bat_crit":            "Connect the charger or the router powers off",
		"failed":              "Failed",
		"error":               "Error: %s",
		"action_done":         "Done",
		"action_failed":       "The action failed",
		"no_data":             "no data",
		"no_internet":         "No internet",
		"no_route":            "no default route",
		"airplane":            "Airplane mode",
		"airplane_sub":        "cellular is off",
		"mobile":              "Mobile network",
		"cable":               "Cable",
		"usb_modem":           "USB modem",
		"via":                 "Via %s",
		"devices":             "Devices",
		"sms":                 "SMS",
		"sms_new":             "%d new",
		"empty":               "Nothing here",
		"no_page":             "Page not found",
		"net_title":           "Network",
		"sec_cell":            "Cellular",
		"sec_inet":            "Internet",
		"sec_router":          "Router",
		"carrier":             "Carrier",
		"network":             "Network",
		"signal":              "Signal",
		"bands":               "Bands",
		"wan":                 "Connection",
		"speed":               "Speed",
		"battery":             "Battery",
		"charging":            "charging",
		"plugged":             "on charger",
		"firmware":            "Firmware",
		"uptime":              "Uptime",
		"temp":                "Temperature",
		"mbit":                "Mbit/s",
		"wifi_uplink":         "Internet over Wi-Fi",
		"wifi_state":          "Network",
		"wifi_networks":       "Wi-Fi networks",
		"wifi_connecting":     "Connecting to %s…",
		"wifi_not_connected":  "not used",
		"wifi_disconnect":     "Disconnect",
		"wifi_disconnect_q":   "Stop taking the internet from this Wi-Fi network?",
		"wifi_scan":           "Search again",
		"wifi_scanning":       "Searching…",
		"wifi_none":           "No networks found",
		"wifi_found":          "Networks: %d",
		"wifi_join_q":         "Join %s?",
		"wifi_join":           "Join",
		"wifi_joined":         "Connected to %s",
		"wifi_failed":         "Could not join %s",
		"wifi_failed_key":     "Could not join %s: wrong password?",
		"wifi_saved":          "saved",
		"wifi_open":           "open",
		"wifi_current":        "in use",
		"wifi_eap":            "This network wants a user name: join it in the web admin",
		"wifi_hint":           "The router takes the internet from the network you join, in place of the SIM. Its own Wi-Fi stays as it is.",
		"ghz":                 "GHz",
		"kbd_password":        "Password",
		"kbd_for":             "Wi-Fi: %s",
		"kbd_hide":            "Hide",
		"kbd_show":            "Show",
		"kbd_empty":           "password",
		"kbd_join":            "Join",
		"kbd_space":           "space",
		"airplane_q":          "Turn airplane mode on?",
		"airplane_note":       "The cellular radio goes off: no mobile data, calls or SMS through the SIM until you turn it off. Wi-Fi stays on.",
		"airplane_on":         "Turn on",
		"airplane_sub_toggle": "cellular off; Wi-Fi stays on",
		"kbit":                "kbit/s",
		"dur_lt":              "under a minute",
		"dur_m":               "%d min",
		"dur_h":               "%d h %d min",
		"dur_d":               "%d d %d h",
		"date":                "%[1]s, %[3]s %[2]d",
		"not_connected":       "not connected",
		"loading":             "Loading…",
		"settings":            "Settings",
		"sms_title":           "SMS",
		"sms_inbox":           "Received (%d)",
		"sms_sent":            "Sent (%d)",
		"sms_none":            "No messages",
		"sms_mark_all":        "Mark all read",
		"sms_delete":          "Delete",
		"sms_delete_q":        "Delete this message?",
		"sms_deleted":         "Deleted",
		"sms_from":            "From",
		"sms_to":              "To",
		"sms_date":            "Date",
		"sms_text":            "Message",
		"sms_hidden":          "Hidden number",
		"sms_new_toast":       "New SMS",
		"sms_error":           "The messages cannot be read: %s",
		"language":            "Language",
		"lang_web":            "Web admin",
		"lang_note":           "Apps change their language as soon as they answer, in a second or two.",
		"about":               "About",
		"apps":                "Apps",
		"stock_screen":        "Stock screen",
		"confirm_stock":       "Switch to the stock screen?",
		"stock_note":          "To come back: mudi-screen-mode custom",
	},
	"ru": {
		"tap_to_open":         "Коснитесь, чтобы открыть",
		"enter_code":          "Введите код",
		"wrong_code":          "Неверный код",
		"wait_n":              "Слишком много попыток, ждите %d с",
		"cancel":              "Отмена",
		"ok":                  "OK",
		"open":                "Открыть",
		"yes":                 "Да",
		"power":               "Питание",
		"power_off":           "Выключить",
		"reboot":              "Перезагрузить",
		"confirm_off":         "Выключить роутер?",
		"confirm_rb":          "Перезагрузить роутер?",
		"power_note":          "Интернет пропадёт",
		"bye_off":             "Выключаюсь…",
		"bye_reboot":          "Перезагружаюсь…",
		"stock":               "Стандартный экран…",
		"charger_on":          "Зарядка подключена",
		"charger_off":         "Зарядка отключена",
		"bat_low_t":           "Низкий заряд",
		"bat_low":             "Подключите зарядку",
		"bat_crit_t":          "Батарея почти разряжена",
		"bat_crit":            "Подключите зарядку, иначе роутер выключится",
		"failed":              "Не получилось",
		"error":               "Ошибка: %s",
		"action_done":         "Готово",
		"action_failed":       "Действие не удалось",
		"no_data":             "нет данных",
		"no_internet":         "Нет интернета",
		"no_route":            "нет маршрута по умолчанию",
		"airplane":            "Авиарежим",
		"airplane_sub":        "сотовая связь выключена",
		"mobile":              "Мобильная сеть",
		"cable":               "Кабель",
		"usb_modem":           "USB-модем",
		"via":                 "Через %s",
		"devices":             "Устройства",
		"sms":                 "SMS",
		"sms_new":             "%d нов.",
		"empty":               "Пусто",
		"no_page":             "Страница не найдена",
		"net_title":           "Сеть",
		"sec_cell":            "Сотовая связь",
		"sec_inet":            "Интернет",
		"sec_router":          "Роутер",
		"carrier":             "Оператор",
		"network":             "Сеть",
		"signal":              "Сигнал",
		"bands":               "Полосы",
		"wan":                 "Подключение",
		"speed":               "Скорость",
		"battery":             "Батарея",
		"charging":            "заряжается",
		"plugged":             "на зарядке",
		"firmware":            "Прошивка",
		"uptime":              "Работает",
		"temp":                "Температура",
		"mbit":                "Мбит/с",
		"wifi_uplink":         "Интернет по Wi-Fi",
		"wifi_state":          "Сеть",
		"wifi_networks":       "Сети Wi-Fi",
		"wifi_connecting":     "Подключаюсь к %s…",
		"wifi_not_connected":  "не используется",
		"wifi_disconnect":     "Отключить",
		"wifi_disconnect_q":   "Перестать брать интернет из этой сети Wi-Fi?",
		"wifi_scan":           "Искать снова",
		"wifi_scanning":       "Ищу сети…",
		"wifi_none":           "Сетей не найдено",
		"wifi_found":          "Сети: %d",
		"wifi_join_q":         "Подключиться к %s?",
		"wifi_join":           "Подключить",
		"wifi_joined":         "Подключено к %s",
		"wifi_failed":         "Не удалось подключиться к %s",
		"wifi_failed_key":     "Не удалось подключиться к %s: неверный пароль?",
		"wifi_saved":          "сохранена",
		"wifi_open":           "открытая",
		"wifi_current":        "сейчас",
		"wifi_eap":            "Этой сети нужен логин: подключите её в веб-админке",
		"wifi_hint":           "Роутер будет брать интернет из выбранной сети вместо SIM. Его собственный Wi-Fi работает как прежде.",
		"ghz":                 "ГГц",
		"kbd_password":        "Пароль",
		"kbd_for":             "Wi-Fi: %s",
		"kbd_hide":            "Скрыть",
		"kbd_show":            "Показать",
		"kbd_empty":           "пароль",
		"kbd_join":            "Готово",
		"kbd_space":           "пробел",
		"airplane_q":          "Включить авиарежим?",
		"airplane_note":       "Сотовый модуль выключится: ни мобильного интернета, ни звонков, ни SMS через SIM, пока не выключите. Wi-Fi продолжит работать.",
		"airplane_on":         "Включить",
		"airplane_sub_toggle": "только сотовая связь",
		"kbit":                "Кбит/с",
		"dur_lt":              "меньше минуты",
		"dur_m":               "%d мин",
		"dur_h":               "%d ч %d мин",
		"dur_d":               "%d д %d ч",
		"date":                "%[1]s, %[2]d %[3]s",
		"not_connected":       "не подключены",
		"loading":             "Загрузка…",
		"settings":            "Настройки",
		"sms_title":           "SMS",
		"sms_inbox":           "Входящие (%d)",
		"sms_sent":            "Отправленные (%d)",
		"sms_none":            "Сообщений нет",
		"sms_mark_all":        "Прочитать все",
		"sms_delete":          "Удалить",
		"sms_delete_q":        "Удалить сообщение?",
		"sms_deleted":         "Удалено",
		"sms_from":            "От",
		"sms_to":              "Кому",
		"sms_date":            "Дата",
		"sms_text":            "Текст",
		"sms_hidden":          "Скрытый номер",
		"sms_new_toast":       "Новое SMS",
		"sms_error":           "Сообщения не читаются: %s",
		"language":            "Язык",
		"lang_web":            "Как в админке",
		"lang_note":           "Приложения сменят язык, как только ответят, через секунду-две.",
		"about":               "О программе",
		"apps":                "Приложения",
		"stock_screen":        "Стандартный экран",
		"confirm_stock":       "Вернуть стандартный экран?",
		"stock_note":          "Вернуть этот: mudi-screen-mode custom",
	},
}

var weekdayNames = map[string][]string{
	"en": {"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"},
	"ru": {"воскресенье", "понедельник", "вторник", "среда", "четверг", "пятница", "суббота"},
}

var monthNames = map[string][]string{
	"en": {"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
	"ru": {"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"},
}

func supportedLang(l string) bool {
	for _, x := range languages {
		if x == l {
			return true
		}
	}
	return false
}

func (s *Screen) tr(key string) string {
	if v, ok := dict[s.lang][key]; ok {
		return v
	}
	if v, ok := dict["en"][key]; ok {
		return v
	}
	return key
}

func (s *Screen) trf(key string, args ...any) string { return fmt.Sprintf(s.tr(key), args...) }

func (s *Screen) dateLine(t time.Time) string {
	wd := weekdayNames[s.lang]
	mn := monthNames[s.lang]
	if wd == nil {
		wd, mn = weekdayNames["en"], monthNames["en"]
	}
	return fmt.Sprintf(s.tr("date"), wd[t.Weekday()], t.Day(), mn[t.Month()-1])
}

func (s *Screen) duration(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Minute {
		return s.tr("dur_lt")
	}
	days := int(d / (24 * time.Hour))
	h := int(d/time.Hour) % 24
	m := int(d/time.Minute) % 60
	switch {
	case days > 0:
		return s.trf("dur_d", days, h)
	case h > 0:
		return s.trf("dur_h", h, m)
	}
	return s.trf("dur_m", m)
}
