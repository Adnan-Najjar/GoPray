package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

type Metadata struct {
	LastUpdate string `json:"LastUpdate"`
	CityName   string `json:"CityName"`
	CityID     int    `json:"CityID"`
}

type PrayerTimes struct {
	Metadata
	Times map[string]string `json:"Times"`
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
	resp, err := http.Get("http://ip-api.com/line?fields=city")
	if err != nil || resp.StatusCode != 200 {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return strings.Trim(string(body), "\n"), nil
}

func getCityID(city string) (int, error) {
	geoname_url := fmt.Sprintf("http://api.geonames.org/searchJSON?q=%s&maxRows=1&username=example", city)
	resp, err := http.Get(geoname_url)
	if err != nil || resp.StatusCode != 200 {
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

	var prayer_times PrayerTimes
	prayer_times.Times = make(map[string]string)
	for i := range matches {
		athaans := strings.Split(matches[i][1], "</td><td>")
		prayer_times.Times[athaans[0]] = athaans[1]
	}
	prayer_times.LastUpdate = time.Now().Format("2006-01-02")
	return prayer_times, nil
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
	screenLocker := getOSCommand()

	config["Fajr"] = PrayerConfig{
		Message: "Fajr Atha'an",
		After: ReminderConfig{
			Reminder: 15,
			Message:  "Fajr Iqa'ama",
			Command:  screenLocker,
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
			Command:  screenLocker,
		},
	}

	config["Asr"] = PrayerConfig{
		Message: "Asr Atha'an",
		After: ReminderConfig{
			Reminder: 15,
			Message:  "Asr Iqa'ama",
			Command:  screenLocker,
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
			Command:  screenLocker,
		},
	}

	config["Isha"] = PrayerConfig{
		Message: "Isha Atha'an",
		After: ReminderConfig{
			Reminder: 15,
			Message:  "Isha Iqa'ama",
			Command:  screenLocker,
		},
	}

	return config
}

func athaanCaller(reminder time.Duration, prayer_name string, config Config) {
	prayer_config := config[prayer_name]

	// TODO: Implement Actions before prayer time

	// Actions at prayer time
	time.Sleep(reminder)
	fmt.Println(prayer_config.Message)
	// Run a command at prayer time
	command := prayer_config.Command
	if len(command) != 0 {
		err := exec.Command(command[0], command[1:]...).Run()
		if err != nil {
			return
		}
	}

	// Actions after prayer time
	after_reminder := time.Duration(prayer_config.After.Reminder) * time.Minute
	time.Sleep(after_reminder)
	fmt.Println(prayer_config.After.Message)
	// Run a command after the reminder
	after_command := prayer_config.After.Command
	if len(after_command) != 0 {
		err := exec.Command(after_command[0], after_command[1:]...).Run()
		if err != nil {
			return
		}
	}
}

func tester(prayer_times PrayerTimes, config Config) {
	for p := range prayer_times.Times {
		now := time.Now()
		// Parse athaan times
		athaan_time, err := time.Parse("3:04 PM", prayer_times.Times[p])
		if err != nil {
			return
		}
		// Use current date with parsed athaan time
		athaan_time_date := time.Date(now.Year(), now.Month(), now.Day(), athaan_time.Hour(), athaan_time.Minute(), 0, 0, now.Location())

		if now.Before(athaan_time_date) {
			duration := athaan_time_date.Sub(now)
			fmt.Printf("Waiting for %s Atha'an after %v\n", p, duration)
			athaanCaller(duration, p, config)
		}
	}
}

func main() {
	var prayer_times PrayerTimes
	var config Config
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

		// Use saved city ID if city didn't change
		var cityId int
		if cityName == prayer_times.CityName {
			cityId = prayer_times.CityID
		} else {
			// Get city ID using geonames.org api
			cityId, err = getCityID(cityName)
			if err != nil {
				fmt.Printf("Error getting city id: %v\n", err)
				return
			}
		}

		// Get prayer times using muslimpro.com
		prayer_times, err = getPrayerTimes(cityId)
		if err != nil {
			fmt.Printf("Error getting prayer times: %v\n", err)
			return
		}

		// Save City id and name for later use
		prayer_times.CityName = cityName
		prayer_times.CityID = cityId
		// Save prayer times in a json file
		err = saveJSON(times_filename, prayer_times)
		if err != nil {
			fmt.Printf("Error saving prayer times to %s: %v\n", times_filename, err)
			return
		}
	}

	// Read config or create it if it doesn't exist
	config, err = readJSON[Config](config_filename)
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

	tester(prayer_times, config)
}
