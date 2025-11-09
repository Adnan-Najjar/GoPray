package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

type PrayerTimes struct {
	LastUpdate string `json:"LastUpdate"`
	Fajr       string `json:"Fajr"`
	Sunrise    string `json:"Sunrise"`
	Zuhr       string `json:"Zuhr"`
	Asr        string `json:"Asr"`
	Maghrib    string `json:"Maghrib"`
	Isha       string `json:"Isha"`
}

type ReminderConfig struct {
	Message  string   `json:"Message"`
	Reminder int      `json:"Reminder"`
	Command  []string `json:"Command"`
}

type PrayerConfig struct {
	Message string         `json:"Message"`
	Command []string       `json:"Command"`
	Before  ReminderConfig `json:"Before"`
	After   ReminderConfig `json:"After"`
}

type Config map[string]PrayerConfig

func getCityName() (string, error) {
	resp, err := http.Get("https://ipapi.co/city/")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body), nil
}

func getCityID(city string) (int, error) {
	geoname_url := fmt.Sprintf("http://api.geonames.org/searchJSON?q=%s&maxRows=1&username=example", city)
	resp, err := http.Get(geoname_url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var data = struct {
		Geonames []struct {
			GeonameID int `json:"geonameId"`
		} `json:"geonames"`
	}{}

	err = json.Unmarshal(body, &data)
	if err != nil {
		return 0, err
	}

	if len(data.Geonames) == 0 {
		return 0, fmt.Errorf("No geonames found for city: %s", city)
	}
	return data.Geonames[0].GeonameID, nil
}

func getPrayerTimes(cityId int) (PrayerTimes, error) {
	muslimpro_url := fmt.Sprintf("https://app.muslimpro.com/muslimprowidget.js?cityid=%d", cityId)
	ctx, cancel := chromedp.NewContext(context.Background())
	defer cancel()

	var jsContent string
	err := chromedp.Run(ctx,
		chromedp.Navigate(muslimpro_url),
		chromedp.WaitReady("body"),
		chromedp.Evaluate(`document.body.innerText`, &jsContent),
	)
	if err != nil {
		return PrayerTimes{}, err
	}

	re := regexp.MustCompile(`<td>(.*)</td>`)
	matches := re.FindAllStringSubmatch(jsContent, -1)

	var p PrayerTimes
	for i := range matches {
		times := strings.Split(matches[i][1], "</td><td>")
		switch times[0] {
		case "Fajr":
			p.Fajr = times[1]
		case "Sunrise":
			p.Sunrise = times[1]
		case "Zuhr":
			p.Zuhr = times[1]
		case "Asr":
			p.Asr = times[1]
		case "Maghrib":
			p.Maghrib = times[1]
		case "Isha":
			p.Isha = times[1]
		}
	}
	p.LastUpdate = time.Now().Format("2006-01-02")
	return p, nil
}

func saveJSON(filename string, data any) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(data); err != nil {
		return err
	}
	return nil
}

func readJSON[T any](filename string) (T, error) {
	var data T
	file, err := os.Open(filename)
	if err != nil {
		return data, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)

	if err := decoder.Decode(&data); err != nil {
		return data, err
	}
	return data, nil
}

func getOSCommand() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{"rundll32.exe", "user32.dll,LockWorkStation"}
	case "linux":
		return []string{"loginctl", "lock-session"}
	case "darwin":
		return []string{"osascript", "-e", `tell application "System Events" to keystroke "q" using {control down, command down}`}
	default:
		return []string{}
	}
}

func defaultConfig() Config {
	config := make(Config)
	lockScreener := getOSCommand()

	config["Fajr"] = PrayerConfig{
		Message: "Fajr Atha'an",
		After: ReminderConfig{
			Reminder: 15,
			Message:  "Fajr Iqa'ama",
			Command:  lockScreener,
		},
	}

	config["Sunrise"] = PrayerConfig{
		Message: "Sun has risen",
		After: ReminderConfig{
			Reminder: 20,
			Message:  "Duhaa time started",
		},
	}

	// 1-2 hour before Zuhr is the best time for Duhaa prayer
	config["Zuhr"] = PrayerConfig{
		Before: ReminderConfig{
			Reminder: 90,
			Message:  "Duhaa prayer",
		},
		Message: "Zuhr Atha'an",
		After: ReminderConfig{
			Reminder: 15,
			Message:  "Zuhr Iqa'ama",
			Command:  lockScreener,
		},
	}

	config["Asr"] = PrayerConfig{
		Message: "Asr Atha'an",
		After: ReminderConfig{
			Reminder: 15,
			Message:  "Asr Iqa'ama",
			Command:  lockScreener,
		},
	}

	config["Maghrib"] = PrayerConfig{
		Before: ReminderConfig{
			Reminder: 5,
			Message:  "Maghrib Atha'an after 5 minutes",
		},
		Message: "Maghrib Atha'an",
		After: ReminderConfig{
			Reminder: 5,
			Message:  "Maghrib Iqa'ama",
			Command:  lockScreener,
		},
	}

	config["Isha"] = PrayerConfig{
		Message: "Isha Atha'an",
		After: ReminderConfig{
			Reminder: 15,
			Message:  "Isha Iqa'ama",
			Command:  lockScreener,
		},
	}

	return config
}

func main() {
	var prayer_times PrayerTimes
	var err error
	times_filename := "prayer_times.json"
	config_filename := "config.json"

	// Read saved prayer times json file
	prayer_times, err = readJSON[PrayerTimes](times_filename)
	// If reading failed OR prayer times not up to date
	// Then update it
	if err != nil || prayer_times.LastUpdate != time.Now().Format("2006-01-02") {

		// Get city name using ipapi.co
		cityName, err := getCityName()
		if err != nil {
			fmt.Printf("Error getting city name: %v\n", err)
			return
		}

		// Get city ID using geonames.org api
		cityId, err := getCityID(cityName)
		if err != nil {
			fmt.Printf("Error getting city id: %v\n", err)
			return
		}

		// Get prayer times using muslimpro.com
		prayer_times, err = getPrayerTimes(cityId)
		if err != nil {
			fmt.Printf("Error getting prayer times: %v\n", err)
			return
		}

		// Save prayer times in a json file
		err = saveJSON(times_filename, prayer_times)
		if err != nil {
			fmt.Printf("Error saving prayer times to %s: %v\n", times_filename, err)
			return
		}
	}

	config, err := readJSON[Config](config_filename)
	if err != nil {
		// Create default config
		config = defaultConfig()

		// Save config in a json file
		err = saveJSON(config_filename, config)
		if err != nil {
			fmt.Printf("Error saving config to %s: %v\n", config_filename, err)
			return
		}
	}
}
