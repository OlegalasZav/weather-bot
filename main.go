package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	"github.com/patrickmn/go-cache"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type Config struct {
	TelegramToken string
	WeatherAPIKey string
}

func NewConfig() *Config {
	err := godotenv.Load()
	if err != nil {
		log.Println("⚠️ .env файл не найден — используем переменные окружения")
	}
	return &Config{
		TelegramToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		WeatherAPIKey: os.Getenv("OPENWEATHER_API_KEY"),
	}
}

type WeatherData struct {
	Name string `json:"name"`
	Main struct {
		Temp      float64 `json:"temp"`
		FeelsLike float64 `json:"feels_like"`
		Humidity  int     `json:"humidity"`
	} `json:"main"`
	Weather []struct {
		ID          int    `json:"id"`
		Main        string `json:"main"`
		Description string `json:"description"`
		Icon        string `json:"icon"`
	} `json:"weather"`
	Wind struct {
		Speed float64 `json:"speed"`
	} `json:"wind"`
	Timezone int `json:"timezone"`
	Dt       int `json:"dt"`
}

var (
	CityMap = map[string]string{
		"/moscow":        "Москва",
		"/spb":           "Санкт-Петербург",
		"/novosibirsk":   "Новосибирск",
		"/yekaterinburg": "Екатеринбург",
		"/kazan":         "Казань",
		"/anadyr":        "Анадырь",
	}
	WeatherIcon = map[string]string{
		"01d": "☀️", "01n": "🌙",
		"02d": "⛅", "02n": "⛅",
		"03d": "☁️", "03n": "☁️",
		"04d": "☁️", "04n": "☁️",
		"09d": "🌧️", "09n": "🌧️",
		"10d": "🌦️", "10n": "🌦️",
		"11d": "⛈️", "11n": "⛈️",
		"13d": "🌨️", "13n": "🌨️",
		"50d": "🌫️", "50n": "🌫️",
	}
	weatherCache = cache.New(10*time.Minute, 15*time.Minute)
)

func GetWeather(ctx context.Context, city string, apiKey string) (*WeatherData, error) {
	cacheKey := fmt.Sprintf("weather:%s:%d", city, time.Now().Truncate(10*time.Minute).Unix())
	if cached, found := weatherCache.Get(cacheKey); found {
		log.Printf("📦 Кэш хит для %s", city)
		return cached.(*WeatherData), nil
	}

	if strings.TrimSpace(city) == "" {
		return nil, fmt.Errorf("название города не может быть пустым")
	}
	baseURL := "https://api.openweathermap.org/data/2.5/weather"
	params := url.Values{}
	params.Set("q", city+",RU")
	params.Set("appid", apiKey)
	params.Set("units", "metric")
	params.Set("lang", "ru")
	url := baseURL + "?" + params.Encode()
	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("ошибка создания запроса: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ошибка HTTP-запроса: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ошибка API: %d", resp.StatusCode)
	}
	var weather WeatherData
	if err := json.NewDecoder(resp.Body).Decode(&weather); err != nil {
		return nil, fmt.Errorf("ошибка парсинга JSON: %w", err)
	}
	if weather.Name == "" {
		return nil, fmt.Errorf("город не найден: %s", city)
	}
	weatherCache.Set(cacheKey, &weather, cache.DefaultExpiration)
	return &weather, nil
}

func FormatWeatherMessage(w *WeatherData) string {
	desc := w.Weather[0].Description
	iconCode := w.Weather[0].Icon
	icon := WeatherIcon[iconCode]
	if icon == "" {
		icon = "🌡️"
	}
	cityName := cases.Title(language.Russian).String(w.Name)
	utcTime := time.Unix(int64(w.Dt), 0).UTC()
	localOffset := time.Duration(w.Timezone) * time.Second
	localTime := utcTime.Add(localOffset)
	readableTime := localTime.Format("15:04")
	temp := int(round(w.Main.Temp))
	feelsLike := int(round(w.Main.FeelsLike))
	humidity := w.Main.Humidity
	windSpeed := w.Wind.Speed

	baseMsg := fmt.Sprintf(
		"🌍 *%s* сейчас (%s):\n"+
			"%s %s %s\n"+
			"Температура: %d°C (ощущается как %d°C)\n"+
			"Влажность: %d%%\n"+
			"Ветер: %.1f м/с",
		cityName,
		readableTime,
		cases.Title(language.Russian).String(desc),
		icon, icon,
		temp,
		feelsLike,
		humidity,
		windSpeed,
	)

	var tip string
	switch {
	case strings.Contains(strings.ToLower(desc), "дождь"):
		tip = " ☔ Льёт как из ведра! Зонт бери или танцуй под ливнем, как в клипе! 💃"
	case strings.Contains(strings.ToLower(desc), "снег"):
		tip = " ❄️ Снежок идёт! Лепи снеговика или греми чайник для какао! ☕⛄"
	case strings.Contains(strings.ToLower(desc), "гроз"):
		tip = " ⛈️ Гром гремит! Сиди дома, смотри кино, молния — не твой бро! 😬"
	case temp > 30:
		tip = " 🔥 Пекло! Хватай мороженое и ныряй в тень, бро! 🍦🌴"
	case temp > 25:
		tip = " ☀️ Жарковато! Коктейль в парке или кондей на полную? Выбирай wisely! 🍹"
	case temp < -10:
		tip = " 🥶 Ледяной апокалипсис! Укутайся, как пингвин, и пей горячий чай! 🧣☕"
	case temp < 0:
		tip = " ❄️ Холодрыга! Шарф, шапка и тёплые носки — твой must-have! 🧦"
	case humidity > 80:
		tip = " 💧 Влажность зашкаливает! Крем от сырости или просто chill у воды? 🌊"
	case windSpeed > 15:
		tip = " 🌪️ Ветрище штормовой! Держи шляпу и не улети, как Карлсон! 🚁"
	case windSpeed > 10:
		tip = " 💨 Ветер крепкий! Завяжи шнурки потуже, а то унесёт к приключениям! 😎"
	case strings.Contains(strings.ToLower(desc), "ясно"):
		tip = " 🌞 Солнце сияет! Хватай очки и гуляй, пока погода шепчет! 😎🚶‍♂️"
	default:
		tip = " 😎 Погода — кайф! Выходи на улицу, лови вайб и наслаждайся! 🌳🎉"
	}

	return baseMsg + tip
}

func round(f float64) float64 {
	if f >= 0 {
		return float64(int(f + 0.5))
	}
	return float64(int(f - 0.5))
}

func main() {
	cfg := NewConfig()
	if cfg.TelegramToken == "" {
		log.Fatal("❌ TELEGRAM_BOT_TOKEN не задан. Добавь его в .env")
	}
	if cfg.WeatherAPIKey == "" {
		log.Fatal("❌ OPENWEATHER_API_KEY не задан. Добавь его в .env")
	}
	bot, err := tgbotapi.NewBotAPI(cfg.TelegramToken)
	if err != nil {
		log.Fatal("❌ Не удалось создать бота:", err)
	}
	log.Printf("✅ Бот @%s запущен! (команды с меню)", bot.Self.UserName)

	// Настройка команд с retry
	for attempt := 0; attempt < 3; attempt++ {
		commands := tgbotapi.NewSetMyCommands(
			tgbotapi.BotCommand{Command: "/start", Description: "Запустить бота"},
			tgbotapi.BotCommand{Command: "/help", Description: "Список команд"},
			tgbotapi.BotCommand{Command: "/moscow", Description: "Погода в Москве"},
			tgbotapi.BotCommand{Command: "/spb", Description: "Погода в Санкт-Петербурге"},
			tgbotapi.BotCommand{Command: "/novosibirsk", Description: "Погода в Новосибирске"},
			tgbotapi.BotCommand{Command: "/yekaterinburg", Description: "Погода в Екатеринбурге"},
			tgbotapi.BotCommand{Command: "/kazan", Description: "Погода в Казани"},
			tgbotapi.BotCommand{Command: "/anadyr", Description: "Погода в Анадыре"},
		)
		resp, err := bot.Request(commands)
		if err == nil && resp.Ok {
			log.Printf("✅ Команды успешно установлены")
			break
		}
		log.Printf("⚠️ Ошибка настройки команд (попытка %d): %v", attempt+1, err)
		if attempt < 2 {
			time.Sleep(time.Duration(2<<attempt) * time.Second)
		} else {
			log.Printf("❌ Не удалось установить команды после 3 попыток")
		}
	}

	updateConfig := tgbotapi.NewUpdate(0)
	updateConfig.Timeout = 60
	updates := bot.GetUpdatesChan(updateConfig)
	for update := range updates {
		if update.Message != nil && update.Message.Text != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)

			text := strings.ToLower(strings.TrimSpace(update.Message.Text))
			city := ""

			// Проверяем команды
			if text == "/start" || text == "/help" {
				msg := tgbotapi.NewMessage(update.Message.Chat.ID,
					"🌍 *Привет, бро!* Я твой погодный гид по России! ☀️ Хочешь знать, брать ли зонт в Питере или шорты в Казани? Пиши город (например, Москва) или жми команды:\n"+
						"/moscow — Погода в Москве\n"+
						"/spb — Погода в Санкт-Петербурге\n"+
						"/novosibirsk — Погода в Новосибирске\n"+
						"/yekaterinburg — Погода в Екатеринбурге\n"+
						"/kazan — Погода в Казани\n"+
						"/anadyr — Погода в Анадыре\n"+
						"/help — Показать это снова\n"+
						"Лови вайб и погоду! 😎🚶‍♂️")
				msg.ParseMode = tgbotapi.ModeMarkdown
				_, err := bot.Send(msg)
				if err != nil {
					log.Printf("❌ Ошибка отправки подсказки: %v", err)
				}
				cancel() // Явный вызов вместо defer
				continue
			} else if cityName, ok := CityMap[text]; ok {
				city = cityName
			} else {
				city = update.Message.Text // Ввод вручную
			}

			weather, err := GetWeather(ctx, city, cfg.WeatherAPIKey)
			if err != nil {
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, fmt.Sprintf("❌ Ошибка: %v", err))
				_, sendErr := bot.Send(msg)
				if sendErr != nil {
					log.Printf("❌ Ошибка отправки ошибки: %v", sendErr)
				}
				cancel() // Явный вызов вместо defer
				continue
			}

			msg := tgbotapi.NewMessage(update.Message.Chat.ID, FormatWeatherMessage(weather))
			msg.ParseMode = tgbotapi.ModeMarkdown
			_, err = bot.Send(msg)
			if err != nil {
				log.Printf("❌ Ошибка отправки погоды: %v", err)
			}
			cancel() // Явный вызов вместо defer
		}
	}
}
