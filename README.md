# GoPray

Lightweight prayer times program that can be customized with reminders, sounds and commands after and before an Atha'an 

## Features

- Automatic location detection and prayer time fetching using [muslimpro](https://muslimpro.com)
- System tray interface showing prayer times
- Notifications for prayer times and reminders
- Plays athaan sound (included)
- Configurable reminders and commands (e.g., screen lock)
- Cross-platform support (Windows, Linux, macOS)

## Configuration

The app creates configuration files in the following directories:

- **Windows**: `%APPDATA%\gopray`
- **Linux**: `~/.config/gopray`
- **macOS**: `~/Library/Application Support/gopray`

The `config.json` file contains settings for each prayer:

```json
{
  "Fajr": {
    "Message": "Fajr Atha'an",
    "Command": [],
    "Sound": "",
    "Before": {
      "Message": "",
      "Reminder": 0,
      "Command": [],
      "Sound": ""
    },
    "After": {
      "Message": "Fajr Iqa'ama",
      "Reminder": 15,
      "Command": ["loginctl", "lock-session"],
      "Sound": ""
    }
  },
  // ... other prayers
}
```

- **Message**: Notification text for the reminder.
- **Command**: Array of strings for a command to run (e.g., screen lock command).
- **Sound**: Path to a custom MP3 file (leave empty for default athaan).
- **Before/After**: Reminders before or after the prayer time.
- **Reminder**: Minutes before/after to trigger the reminder (0 to disable).

You must restart to apply changes.

## TODO

- [x] Add an indicator that shows time until next prayer
- [ ] Implement logging for debugging
- [ ] Add unit tests and improve error handling
- [ ] Add more sound options and volume control
- [ ] Add localization for notifications in different languages
- [ ] Add GUI for configuration instead of editing JSON
