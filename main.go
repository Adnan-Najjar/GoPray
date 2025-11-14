package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/ebitengine/oto/v3"
	"github.com/energye/systray"
	"github.com/gen2brain/beeep"
	"github.com/hajimehoshi/go-mp3"
)

//go:embed assets/*
var assetsFS embed.FS

var Next string
var ctx context.Context
var cancel context.CancelFunc

type Metadata struct {
	LastUpdate string `json:"LastUpdate"`
	CityName   string `json:"CityName"`
	CityID     int    `json:"CityID"`
}

type Prayer struct {
	Name string `json:"name"`
	Time string `json:"time"`
}

type PrayerTimes struct {
	Metadata
	Times []Prayer `json:"Times"`
}

type ReminderConfig struct {
	Message  string   `json:"Message"`
	Reminder int      `json:"Reminder"`
	Command  []string `json:"Command"`
	Sound    string   `json:"Sound"`
}

type PrayerConfig struct {
	ReminderConfig
	Before ReminderConfig `json:"Before"`
	After  ReminderConfig `json:"After"`
}

type Config map[string]PrayerConfig

type PrayerDuration struct {
	Name     string
	Duration time.Duration
}

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
	prayers := []Prayer{}
	for i := range matches {
		name_time := strings.Split(matches[i][1], "</td><td>")
		prayers = append(prayers, Prayer{Name: name_time[0], Time: name_time[1]})
	}
	prayer_times.Times = prayers
	prayer_times.LastUpdate = time.Now().Format("02-01-2006")
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
		ReminderConfig: ReminderConfig{
			Message: "Fajr Atha'an",
			Sound:   "",
		},
		After: ReminderConfig{
			Reminder: 15,
			Message:  "Fajr Iqa'ama",
			Sound:    "",
			Command:  screenLocker,
		},
	}

	config["Sunrise"] = PrayerConfig{
		ReminderConfig: ReminderConfig{
			Message: "Sun has risen",
			Sound:   "",
		},
		After: ReminderConfig{
			Reminder: 20,
			Sound:    "",
			Message:  "Duhaa time started",
		},
	}

	// 1-2 hour before Zuhr is the best time for Duhaa prayer
	config["Zuhr"] = PrayerConfig{
		Before: ReminderConfig{
			Reminder: 90,
			Message:  "Duhaa prayer",
			Sound:    "",
		},
		ReminderConfig: ReminderConfig{
			Message: "Zuhr Atha'an",
		},
		After: ReminderConfig{
			Reminder: 15,
			Message:  "Zuhr Iqa'ama",
			Sound:    "",
			Command:  screenLocker,
		},
	}

	config["Asr"] = PrayerConfig{
		ReminderConfig: ReminderConfig{
			Message: "Asr Atha'an",
			Sound:   "",
		},
		After: ReminderConfig{
			Reminder: 15,
			Message:  "Asr Iqa'ama",
			Sound:    "",
			Command:  screenLocker,
		},
	}

	config["Maghrib"] = PrayerConfig{
		Before: ReminderConfig{
			Reminder: 5,
			Message:  "Maghrib Atha'an after 5 minutes",
			Sound:    "",
		},
		ReminderConfig: ReminderConfig{
			Message: "Maghrib Atha'an",
		},
		After: ReminderConfig{
			Reminder: 5,
			Message:  "Maghrib Iqa'ama",
			Sound:    "",
			Command:  screenLocker,
		},
	}

	config["Isha"] = PrayerConfig{
		ReminderConfig: ReminderConfig{
			Message: "Isha Atha'an",
			Sound:   "",
		},
		After: ReminderConfig{
			Reminder: 15,
			Message:  "Isha Iqa'ama",
			Sound:    "",
			Command:  screenLocker,
		},
	}

	return config
}

// Sends a notification with sound and runs the command
func muezzin(config ReminderConfig, name string, format string) {
	opts := []any{name}
	if config.Reminder != 0 {
		opts = []any{config.Reminder, name}
	}
	message := fmt.Sprintf(format, opts ...)
	beeep.Notify(config.Message, message, "")
	// Play sound
	playMP3(config.Sound)
	// Run a command if exists
	command := config.Command
	if len(command) != 0 {
		err := exec.Command(command[0], command[1:]...).Run()
		if err != nil {
			return
		}
	}
}

func athaanScheduler(ctx context.Context, prayer PrayerDuration, config Config) {
	prayer_config := config[prayer.Name]

	// Actions before prayer time
	before_reminder := prayer.Duration - time.Duration(prayer_config.Before.Reminder)*time.Minute
	if before_reminder > 0 {
		select {
		case <-time.After(before_reminder):
			muezzin(prayer_config.Before, prayer.Name, "%d mins until %s Atha'an")
		case <-ctx.Done():
			return
		}
	}

	// Actions at prayer time
	if prayer.Duration >= 0 {
		select {
		case <-time.After(prayer.Duration):
			muezzin(prayer_config.ReminderConfig, prayer.Name, "%s Atha'an is now")
		case <-ctx.Done():
			return
		}
	}

	// Actions after prayer time
	after_reminder := time.Duration(prayer_config.After.Reminder) * time.Minute
	if after_reminder > 0 {
		select {
		case <-time.After(after_reminder):
			muezzin(prayer_config.Before, prayer.Name, "%d mins until %s Atha'an")
		case <-ctx.Done():
			return
		}
	}
}

func getPrayerDurations(prayer_times PrayerTimes) ([]PrayerDuration, error) {
	var prayer_durations []PrayerDuration
	for _, prayer := range prayer_times.Times {
		now := time.Now()
		// Parse prayer times
		prayer_time, err := time.Parse("3:04 PM", prayer.Time)
		if err != nil {
			return nil, err
		}
		// Use current date with parsed prayer time
		prayer_time_date := time.Date(now.Year(), now.Month(), now.Day(), prayer_time.Hour(), prayer_time.Minute(), 0, 0, now.Location())

		if now.Before(prayer_time_date) {
			duration := prayer_time_date.Sub(now)
			prayer_durations = append(prayer_durations, PrayerDuration{Name: prayer.Name, Duration: duration})
		}
	}

	sort.Slice(prayer_durations, func(i, j int) bool {
		return prayer_durations[i].Duration < prayer_durations[j].Duration
	})

	return prayer_durations, nil
}

var prayer_times PrayerTimes
var prayer_durations []PrayerDuration
var config Config

func runMain() {
	var err error
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = "."
	}
	configDir = filepath.Join(configDir, "gopray")
	os.MkdirAll(configDir, 0755)
	times_filename := filepath.Join(configDir, "prayer_times.json")
	config_filename := filepath.Join(configDir, "config.json")

	ctx, cancel = context.WithCancel(context.Background())

	// Read saved prayer times json file
	prayer_times, err = readJSON[PrayerTimes](times_filename)
	// If reading failed OR prayer times not up to date
	// Then update it
	if err != nil || prayer_times.LastUpdate != time.Now().Format("02-01-2006") {

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

	// Calculate durations (time until prayer) and sort them
	prayer_durations, err = getPrayerDurations(prayer_times)
	if err != nil {
		fmt.Printf("Error getting durations: %v", err)
		return
	}

	fmt.Printf("Times: %v\nDurations: %v\n", prayer_times, prayer_durations)
	for _, p := range prayer_durations {
		go athaanScheduler(ctx, p, config)
	}
}

func onReady() {
	systray.SetIcon(getIcon())
	systray.SetTitle("GoPray")
	systray.SetTooltip("Prayer Times App")

	// Add City name and Date
	text := fmt.Sprintf("%s\t\t\t%s", prayer_times.CityName, prayer_times.LastUpdate)
	systray.AddMenuItem(text, "").Disable()
	systray.AddSeparator()

	// Add each prayer time
	for _, prayer := range prayer_times.Times {
		format := fmt.Sprintf("%%-%ds\t %%s", 25-len(prayer.Name))
		systray.AddMenuItem(fmt.Sprintf(format, prayer.Name, prayer.Time), "")
	}
	systray.AddSeparator()

	// Add Quit button
	quit := systray.AddMenuItem("Quit", "Quit the app")
	quit.Click(func() {
		systray.Quit()
	})
}

func onExit() {
	// kill children to avoid orphanage
	if cancel != nil {
		cancel()
	}
}

func playMP3(filePath string) {
	var file io.ReadCloser
	var err error
	if len(filePath) <= 0 {
		file, err = assetsFS.Open(filePath)
	} else {
		file, err = os.Open(filePath)
	}
	defer file.Close()

	decoder, err := mp3.NewDecoder(file)
	if err != nil {
		return
	}

	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   decoder.SampleRate(),
		ChannelCount: 2,
		Format:       oto.FormatSignedInt16LE,
	})
	if err != nil {
		return
	}
	<-ready

	player := ctx.NewPlayer(decoder)

	player.Play()

	for player.IsPlaying() {
		time.Sleep(100 * time.Millisecond)
	}
}

func getIcon() []byte {
	iconWin, err := assetsFS.ReadFile("assets/icon.ico")
	if err != nil {
		fmt.Printf("Icon failed: %v:", err)
		return nil
	}

	icon, err := assetsFS.ReadFile("assets/icon.png")
	if err != nil {
		fmt.Printf("Icon failed: %v:", err)
		return nil
	}

	if runtime.GOOS == "windows" {
		return iconWin
	} else {
		return icon
	}
}

func main() {
	beeep.AppName = "GoPray"
	runMain()

	// Handle signals to quit gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		systray.Quit()
	}()

	systray.Run(onReady, onExit)
}
