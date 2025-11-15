package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
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

var next_prayer PrayerDuration

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
			Sound:   "athaan",
		},
		After: ReminderConfig{
			Reminder: 15,
			Message:  "Fajr Iqa'ama",
			Command:  screenLocker,
		},
	}

	config["Sunrise"] = PrayerConfig{
		ReminderConfig: ReminderConfig{
			Message: "Sun has risen",
			Sound:   "athaan",
		},
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
		ReminderConfig: ReminderConfig{
			Message: "Zuhr Atha'an",
			Sound:   "athaan",
		},
		After: ReminderConfig{
			Reminder: 15,
			Message:  "Zuhr Iqa'ama",
			Command:  screenLocker,
		},
	}

	config["Asr"] = PrayerConfig{
		ReminderConfig: ReminderConfig{
			Message: "Asr Atha'an",
			Sound:   "athaan",
		},
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
		ReminderConfig: ReminderConfig{
			Message: "Maghrib Atha'an",
			Sound:   "athaan",
		},
		After: ReminderConfig{
			Reminder: 5,
			Message:  "Maghrib Iqa'ama",
			Command:  screenLocker,
		},
	}

	config["Isha"] = PrayerConfig{
		ReminderConfig: ReminderConfig{
			Message: "Isha Atha'an",
			Sound:   "athaan",
		},
		After: ReminderConfig{
			Reminder: 15,
			Message:  "Isha Iqa'ama",
			Command:  screenLocker,
		},
	}

	return config
}

// Sends a notification with sound and runs the command
func muezzin(reminderConfig ReminderConfig) {
	// Send notification
	if len(reminderConfig.Message) > 0 {
		beeep.Notify(beeep.AppName, reminderConfig.Message, getIcon())
	}
	// Play sound
	playMP3(reminderConfig.Sound)
	// Run a command if exists
	command := reminderConfig.Command
	if len(command) != 0 {
		err := exec.Command(command[0], command[1:]...).Run()
		if err != nil {
			return
		}
	}
}

func athaanScheduler(ctx context.Context, prayer PrayerDuration) {
	prayer_config := config[prayer.Name]

	// Actions before prayer time
	before_reminder := prayer.Duration - time.Duration(prayer_config.Before.Reminder)*time.Minute
	if before_reminder > 0 {
		select {
		case <-time.After(prayer.Duration - before_reminder):
			muezzin(prayer_config.Before)
		case <-ctx.Done():
			return
		}
	}

	// Actions at prayer time
	select {
	case <-time.After(prayer.Duration):
		muezzin(prayer_config.ReminderConfig)
		next_prayer = prayer
	case <-ctx.Done():
		return
	}

	// Actions after prayer time
	after_reminder := time.Duration(prayer_config.After.Reminder) * time.Minute
	if after_reminder > 0 {
		select {
		case <-time.After(prayer.Duration + after_reminder):
			muezzin(prayer_config.After)
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
	if len(prayer_durations) > 0 {
		next_prayer = prayer_durations[0]
	}

	return prayer_durations, nil
}

var prayer_times PrayerTimes
var prayer_durations []PrayerDuration
var config Config

var config_path string
var prayertimes_path string

func runMain() {
	var err error
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = "."
	}
	configDir = filepath.Join(configDir, "gopray")
	os.MkdirAll(configDir, 0755)
	prayertimes_path = filepath.Join(configDir, "prayer_times.json")
	config_path = filepath.Join(configDir, "config.json")

	ctx, cancel = context.WithCancel(context.Background())

	// Read saved prayer times json file
	prayer_times, err = readJSON[PrayerTimes](prayertimes_path)
	// If reading failed OR prayer times not up to date
	// Then update it
	if err != nil || prayer_times.LastUpdate != time.Now().Format("02-01-2006") {

		// Get city name using ipapi.co
		cityName, err := getCityName()
		if err != nil {
			log.Printf("ERROR: fetching city name: %v", err)
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
				log.Printf("ERROR: fetching city id: %v", err)
				return
			}
		}

		// Get prayer times using muslimpro.com
		prayer_times, err = getPrayerTimes(cityId)
		if err != nil {
			log.Printf("ERROR: fetching prayer times: %v", err)
			return
		}

		// Save City id and name for later use
		prayer_times.CityName = cityName
		prayer_times.CityID = cityId
		// Save prayer times in a json file
		err = saveJSON(prayertimes_path, prayer_times)
		if err != nil {
			log.Printf("ERROR: saving JSON: %v", err)
			return
		}
	}

	// Read config or create it if it doesn't exist
	config, err = readJSON[Config](config_path)
	if err != nil {
		// Create default config
		config = defaultConfig()

		// Save config in a json file
		err = saveJSON(config_path, config)
		if err != nil {
			log.Printf("ERROR: saving JSON: %v", err)
			return
		}
	}

	// Calculate durations (time until prayer) and sort them
	prayer_durations, err = getPrayerDurations(prayer_times)
	if err != nil {
		log.Printf("ERROR: calculating durations: %v", err)
		return
	}

	log.Printf("Times: %v\nDurations: %v", prayer_times, prayer_durations)
	for _, p := range prayer_durations {
		go athaanScheduler(ctx, p)
	}
}

func onReady() {
	systray.SetIcon(getIcon())
	systray.SetTitle(beeep.AppName)
	systray.SetTooltip("Prayer Times App")
	// Open on left click
	systray.SetOnClick(func(menu systray.IMenu) {
		menu.ShowMenu()
	})

	// Add City name and Date
	text := fmt.Sprintf("%s\t\t\t%s", prayer_times.CityName, prayer_times.LastUpdate)
	systray.AddMenuItem(text, "").Disable()
	systray.AddSeparator()

	// Check if next prayer exist
	if next_prayer != (PrayerDuration{}) {
		// Show time until next prayer
		next_prayer_menu := systray.AddMenuItem("", "")
		go func() {
			for {
				var until string
				if next_prayer.Duration.Minutes() < 1 {
					until = strings.Split(next_prayer.Duration.String(), ".")[0] + "s"
				} else {
					until = strings.Split(next_prayer.Duration.String(), "m")[0] + "m"
				}
				message := fmt.Sprintf("%s until %s", until, next_prayer.Name)
				next_prayer_menu.SetTitle(message)
				time.Sleep(time.Minute)
				next_prayer.Duration = next_prayer.Duration - time.Minute
			}
		}()
		systray.AddSeparator()
	}

	// Add each prayer time
	for _, prayer := range prayer_times.Times {
		format := fmt.Sprintf("%%-%ds\t %%s", 20-len(prayer.Name))
		// Create summary of current config
		var prayer_config string
		reminder := config[prayer.Name].ReminderConfig
		if reminder.Reminder > 0 {
			prayer_config += fmt.Sprintf("%d mins\n", reminder.Reminder)
		}
		if reminder.Message != "" {
			prayer_config += fmt.Sprintf("%s\n", reminder.Message)
		}
		if reminder.Command != nil {
			prayer_config += fmt.Sprintf("%v\n", reminder.Command)
		}

		before := config[prayer.Name].Before
		if before.Reminder > 0 {
			prayer_config += "Before:\n"
			prayer_config += fmt.Sprintf("\t%d mins\n", before.Reminder)
		}
		if before.Message != "" {
			prayer_config += fmt.Sprintf("\t%s\n", before.Message)
		}
		if before.Command != nil {
			prayer_config += fmt.Sprintf("\t%v\n", before.Command)
		}

		after := config[prayer.Name].After
		if after.Reminder > 0 {
			prayer_config += "After:\n"
			prayer_config += fmt.Sprintf("\t%d mins\n", after.Reminder)
		}
		if after.Message != "" {
			prayer_config += fmt.Sprintf("\t%s\n", after.Message)
		}
		if after.Command != nil {
			prayer_config += fmt.Sprintf("\t%v\n", after.Command)
		}

		systray.AddMenuItem(fmt.Sprintf(format, prayer.Name, prayer.Time), "").AddSubMenuItem(prayer_config, "")
	}
	systray.AddSeparator()

	// Shortcut to edit config
	edit_config_menu := systray.AddMenuItem("Edit config", "")
	edit_config_menu.Click(func() {
		// Open config file in default text editor
		switch runtime.GOOS {
		case "windows":
			exec.Command("notepad.exe", config_path).Start()
		case "darwin":
			exec.Command("open", "-e", config_path).Start()
		case "linux":
			exec.Command("xdg-open", config_path).Start()
		}
	})
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
	// Use saved sounds if given file is not .mp3
	if !strings.HasSuffix(filePath, ".mp3") {
		filePath = fmt.Sprintf("assets/%s.mp3", filePath)
		file, err = assetsFS.Open(filePath)
		if err != nil {
			return
		}
	} else { // Use sounds inside config dir
		filePath = filepath.Join(config_path, filePath)
		file, err = os.Open(filePath)
		if err != nil {
			return
		}
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
		log.Printf("ERROR: loading ICO icon: %v", err)
		return nil
	}

	icon, err := assetsFS.ReadFile("assets/icon.png")
	if err != nil {
		log.Printf("ERROR: loading PNG icon: %v", err)
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
