package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
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

func main() {
	var prayer_times PrayerTimes
	var err error
	times_filename := "prayer_times.json"

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
}
