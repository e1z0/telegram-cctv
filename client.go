package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/go-sql-driver/mysql"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/jmoiron/sqlx"
	term "github.com/nsf/termbox-go"
        "github.com/go-redis/redis"
)

var (
	// build info
	Version string
	Build   string
	Commit  string
	// end of build info
	daemonize        bool
        settings         Settings
	bot              *tgbotapi.BotAPI
	images           = map[string]map[string][]byte{}
	last_topic       string
	last_msg         []byte
	OldSnapshot      bool
	OldSnapshotData  Snapshot
	mysqldb          *sqlx.DB
        redisdb                  *redis.Client
)

var numericKeyboard = tgbotapi.NewReplyKeyboard(
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("1"),
		tgbotapi.NewKeyboardButton("2"),
		tgbotapi.NewKeyboardButton("3"),
	),
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("4"),
		tgbotapi.NewKeyboardButton("5"),
		tgbotapi.NewKeyboardButton("6"),
	),
)

func reset() {
	term.Sync() // cosmestic purpose
}

type Snapshot struct {
	Last_topic string `json:"last_topic"`
	Last_msg   []byte `json:"last_msg"`
}

type Settings struct {
        Mqttbroker string `json:"mqttbroker"`
        Dbhost string `json:"dbhost"`
        Dbport string `json:"dbport"`
        Dbuser string `json:"dbuser"`
        DbDb   string `json:"dbdb"`
        Dbpass string `json:"dbpass"`
        TelegramBotToken string `json:"telegrambottoken"`
        TelegramChatId int64 `json:"telegramchatid"`
        NotifyObject  []string `json:"notifyobject"`
}

func LoadSnapshot() {
	ProgramPath, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		panic(err)
	}
	// load settings
        jsonFile, err := os.Open(ProgramPath + "/client.json")
        if err != nil {
		fmt.Printf("Settings file not found, please create one!\n")
        }
        defer jsonFile.Close()
        byteValue, err := ioutil.ReadAll(jsonFile)
        if err != nil {
                fmt.Println(err)
        }
        err = json.Unmarshal(byteValue, &settings)
        if err != nil {
          fmt.Printf("Unable to read program settings: %s\n",err)
          os.Exit(1)
        }
	if _, err := os.Stat(ProgramPath + "/data.dump"); err == nil {
		fmt.Printf("Found data snapshot on the disk: %s, loading data...\n", ProgramPath+"/data.dump")
		jsonFile, err := os.Open(ProgramPath + "/data.dump")
		// if we os.Open returns an error then handle it
		if err != nil {
			fmt.Printf("Unable to open data dump snapshot of the queue: %s\n", err)
		}
		fmt.Printf("Successfully Opened data snapshot from the disk\n")
		// defer the closing of our jsonFile so that we can parse it later on
		defer jsonFile.Close()
		byteValue, _ := ioutil.ReadAll(jsonFile)
		err = json.Unmarshal(byteValue, &OldSnapshotData)
		if err == nil {
			OldSnapshot = true
			last_topic = OldSnapshotData.Last_topic
			last_msg = OldSnapshotData.Last_msg
		} else {
			fmt.Printf("Unable to parse data stored in the disk as the snapshot!\n")
		}
	} else if os.IsNotExist(err) {
		fmt.Printf("Program data does not exist, skipping load snapshot data from the disk!\n")
	}
}

// NullTime is an alias for mysql.NullTime data type
type NullTime mysql.NullTime

// Scan implements the Scanner interface for NullTime
func (nt *NullTime) Scan(value interface{}) error {
	var t mysql.NullTime
	if err := t.Scan(value); err != nil {
		return err
	}

	// if nil then make Valid false
	if reflect.TypeOf(value) == nil {
		*nt = NullTime{t.Time, false}
	} else {
		*nt = NullTime{t.Time, true}
	}

	return nil
}

func dbconn() {

	if mysqldb == nil {
		db, err := sqlx.Connect("mysql", fmt.Sprintf("%s:%s@tcp(%s:%s)/%s", settings.Dbuser, settings.Dbpass, settings.Dbhost, settings.Dbport, settings.DbDb))
		if err != nil {
			fmt.Printf("Got error then tried to contact to mysql server %s\n", err)
			return
		}
		mysqldb = db
		return
	}

	err := mysqldb.Ping()
	if err != nil {
		mysqldb = nil
		fmt.Printf("Unable to ping the MySQL connection, it's lost!! Reconnecting...\n")
		db, err := sqlx.Connect("mysql", fmt.Sprintf("%s:%s@tcp(%s:%s)/%s", settings.Dbuser, settings.Dbpass, settings.Dbhost, settings.Dbport, settings.DbDb))
		if err != nil {
			fmt.Printf("Got error then tried to contact to mysql server %s\n", err)
			return
		}
		fmt.Printf("Reconnection was successfull!\n")
		mysqldb = db
	}
	return
}

func SaveSnapshot() {
	ProgramPath, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		panic(err)
	}
	fmt.Printf("Doing program snapshot...\n")
	datas := Snapshot{Last_topic: last_topic, Last_msg: last_msg}
	r, err := json.Marshal(datas)
	if err != nil {
		fmt.Printf("Unable to serialize data for data dump of the program\n")
		return
	}
	err = ioutil.WriteFile(ProgramPath+"/data.dump", r, 0644)
	if err != nil {
		fmt.Printf("CRITICAL ERROR Saving the Snapshot data to the disk!!!\n")
		return
	}

	fmt.Printf("Program data, sucessfully dumped to the disk snapshot!\n")
}

func Resend() {
	// resends last action
	if last_topic == "" && last_msg == nil {
		fmt.Printf("The last message is currently empty, please wait for some events to accour!\n")
	} else {
		go HandleMsg(last_topic, last_msg)
	}
}

func Finish() {
	fmt.Printf("Finishing the jobs and doing clean shutdown...\n")
	SaveSnapshot()
	os.Exit(0)
}

func DebugShow() {
	fmt.Printf("Here are the stats!\n")
	fmt.Printf("Currently holding images: %v\n", images)
}

func IsNotifable(object string) bool {
	for _, b := range settings.NotifyObject {
		if b == object {
			return true
		}
	}
	return false
}

func Callbackas() {
	fmt.Printf("Useris kazka paspaude, ojoj\n")
}


type Eventas struct {
Object string `db:"object"`
Camera string `db:"camera"`
TimeStamp time.Time `db:"timestamp"`
}

func IncreaseEventCounter() {
_, _ = redisdb.HIncrBy("cameras_events_stats","per_hour",1).Result()
_, _ = redisdb.HIncrBy("cameras_events_stats","per_day",1).Result()
_, _ = redisdb.HIncrBy("cameras_events_stats","per_week",1).Result()
_, _ = redisdb.HIncrBy("cameras_events_stats","per_month",1).Result()
_, _ = redisdb.HIncrBy("cameras_events_stats","per_year",1).Result()
}

func RecordEvent(event Eventas, fakefile []byte) {
IncreaseEventCounter()
eventstor := "/storas/Cameras/Events"
path := eventstor + "/"+event.Camera
if _, err := os.Stat(path); os.IsNotExist(err) {
    os.Mkdir(path, 0755)
}
filename := path+"/"+event.TimeStamp.Format("2006-01-02-15-04-05")+".jpg"
// save the file
err := ioutil.WriteFile(filename, fakefile, 0644)
if err == nil {
  // sql insert
  dbconn()
  query := fmt.Sprintf("INSERT INTO events (object,camera,filename,timestamp) VALUES('%s','%s','%s','%s')", event.Object,event.Camera,filename,event.TimeStamp.Format("2006-01-02 15:04:05"))
  res, erras := mysqldb.Exec(query)
    if erras != nil {
    fmt.Printf("Error inserting event records query %s: %s\n", query, err)
    return
    }
  idas,_ := res.LastInsertId()
  _, err := redisdb.Set("cameras_last_insertid",fmt.Sprintf("%d",idas),0).Result()
  if err != nil {
  fmt.Printf("Got problem in setting the last id to the redis backend!! %s\n",err)
  }
 }
}

func HandleMsg(topic string, msg []byte) {
	last_topic = topic
	last_msg = msg
	data := strings.Split(topic, "/")
	timestamp := time.Now()
	currentTime := timestamp.Format("2006.01.02-15:04:05")
	// temp hack, needs to invent channels here
	time.Sleep(500 * time.Millisecond)
	mesege := fmt.Sprintf("%s -> Detected object: %s movement on camera: %s\n", currentTime, data[2], data[1])
	fmt.Printf(mesege)
	telegrammessage := fmt.Sprintf("Detected: %s on: %s at: %s\n", data[2], data[1], currentTime)
        fakeFile := images[data[1]][data[2]]
        evnt := Eventas{Object: data[2], Camera: data[1], TimeStamp: timestamp}
        RecordEvent(evnt,fakeFile)
	if IsNotifable(data[2]) {
		msga := tgbotapi.NewPhotoUpload(settings.TelegramChatId, tgbotapi.FileBytes{Name: currentTime + ".jpg", Bytes: fakeFile})
		msga.Caption = telegrammessage
		msga.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Show clips", "timelapse#"+timestamp.Format("2006.01.02 15:04:05")),
			),
		)
		bot.Send(msga)
	}
}

func HandleImage(topic string, image []byte) {
	data := strings.Split(topic, "/")
	timestamp := time.Now()
	currentTime := timestamp.Format("2006.01.02-15:04:05")
	fmt.Printf("%s -> Got new image from camera: %s object: %s notifable: %v\n", currentTime, data[1], data[2], IsNotifable(data[2]))
	if IsNotifable(data[2]) {
		if images[data[1]] == nil {
			images[data[1]] = map[string][]byte{}
		}
		images[data[1]][data[2]] = image
	}
}

func messageHandler(c mqtt.Client, msg mqtt.Message) {
	topic := msg.Topic()
	if strings.Contains(topic, "snapshot") {
		go HandleImage(topic, msg.Payload())
	} else {
		go HandleMsg(msg.Topic(), msg.Payload())
	}
}

func connLostHandler(c mqtt.Client, err error) {
	fmt.Printf("Connection lost, reason: %v\n", err)
	//Perform additional action...
}

var replyMarkup = tgbotapi.NewInlineKeyboardMarkup(
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("ReplyKeyboardMarkup", "ReplyKeyboardMarkup"),
		tgbotapi.NewInlineKeyboardButtonData("removeReplyKeyboardMarkup", "removeReplyKeyboardMarkup"),
	),
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("ReplyKeyboardMarkupOneTimeSize", "ReplyKeyboardMarkupOneTimeSize"),
	),
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("InlineKeyboardMarkup", "InlineKeyboardMarkup"),
	),
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("ForceReply", "ForceReply"),
	),
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Markdown_HTML", "Markdown_HTML"),
	),
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Photo", "Photo"),
	),
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("callback_data", "callback_data"),
		tgbotapi.NewInlineKeyboardButtonData("callback_data_alert", "callback_data_alert"),
	),
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("edited text(也可改 markup)", "edited_text"),
	),
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("edited reply markup", "edited_reply_markup"),
	),
)

type TgFile struct {
	Name    string
	Caption string
}

func TGUploadMultiImage(chatid int64, message_id int, failai []TgFile) {
	type TgItemFix struct {
		Type    string `json:"type"`
		Media   string `json:"media"`
		Caption string `json:"caption"`
	}

	var media []TgItemFix
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	for i, v := range failai {
		file, err := os.Open(v.Name)
		if err == nil {
			defer file.Close()
			fi, err := file.Stat()
			// i think here we should check the filesize if it's non zero
			if err == nil {
				part, err := writer.CreateFormFile("photo_"+strconv.Itoa(i+1), fi.Name())
				if err == nil {
					io.Copy(part, file)
					media = append(media, TgItemFix{Type: "video", Media: "attach://photo_" + strconv.Itoa(i+1), Caption: v.Caption})
				} else { // if part is nil
					fmt.Printf("Cannot create file part for multiple image upload to telegram err: %s\n", err)
				}
			} else { // if stat is nil
				fmt.Printf("Cannot stat file: %s on the file system for multiple telegram upload: %s\n", v.Name, err)
			}
		} else { // if file is nil
			fmt.Printf("Cannot open file: %s for the multiple telegram upload err: %s\n", v.Name, err)
		}
	}

	request_url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMediaGroup", settings.TelegramBotToken)
	client := &http.Client{}
	params := map[string]string{"chat_id": strconv.FormatInt(chatid, 10), "reply_to_message_id": strconv.Itoa(message_id)}
	for key, val := range params {
		_ = writer.WriteField(key, val)
	}
	jsonStr, err := json.Marshal(media)
	if err != nil {
		fmt.Printf("Unable to serialize media json for multiple telegram post! %s\n", err)
	}
	_ = writer.WriteField("media", string(jsonStr))
	err = writer.Close()
	if err != nil {
		fmt.Printf("Unable to write fields in multiple telegram post: %s\n", err)
		return
	}

	req, err := http.NewRequest("POST", request_url, body)
	if err != nil {
		fmt.Printf("We failed at to post to telegram multiple message :( %s\n", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := client.Do(req)

	if err != nil {
		fmt.Printf("We have problem posting telegram with code: %d\n", resp.StatusCode)
	} else {
		fmt.Printf("We have sucessfully posted to telegram with code: %d\n", resp.StatusCode)
		var bodyContent = make([]byte, 0)
		fmt.Println(resp.StatusCode)
		fmt.Println(resp.Header)
		resp.Body.Read(bodyContent)
		resp.Body.Close()
		fmt.Println(bodyContent)
	}
}

type SecurityCameras struct {
	Id         int            `db:"id"`
	Camera     int            `db:"camera"`
	Name       string         `db:"name"`
	FileName   string         `db:"filename"`
	Frame      int            `db:"frame"`
	Time_Start mysql.NullTime `db:"time_start"`
	Time_End   mysql.NullTime `db:"time_end"`
	File_Type  int            `db:"file_type"`
}

func QueryTimeEvents(data string) []TgFile {
	var failai []TgFile
	fmt.Printf("Querying database for near date: %s events...\n", data)
	dbconn()
	t, err := time.Parse("2006.01.02 15:04:05", data)
	if err == nil {
		minuciukai := 10
		begin_time := t.Add(time.Duration(-minuciukai) * time.Minute)
		end_time := t.Add(2 * time.Minute)
		fmt.Printf("i've got the time: %s, so i've made the time scale between %s and %s ;-)\n", data, begin_time.Format("2006-01-02 15:04:05"), end_time.Format("2006-01-02 15:04:05"))
		// time from 17 to 17:15h
		// select * from security where time_start > CAST('2020-07-10 17:00' AS DATETIME) AND time_start < CAST('2020-07-10 17:15' AS DATETIME);
		Rows := []SecurityCameras{}
		sql := fmt.Sprintf("select * from security where time_start > CAST('%s' AS DATETIME) AND time_start < CAST('%s' AS DATETIME)", begin_time.Format("2006-01-02 15:04:05"), end_time.Format("2006-01-02 15:04:05"))
		err = mysqldb.Select(&Rows, sql)
		if err != nil {
			fmt.Printf("Got error in mysql query %s\n", err)
		}
		fmt.Printf("After sql select we've got: %v\n", Rows)
		for _, row := range Rows {
			failai = append(failai, TgFile{Name: row.FileName, Caption: row.Name + " > " + row.Time_Start.Time.Format("2006-01-02 15:04:05")})
		}
	} else {
		fmt.Printf("Error while parsing date! %s\n", err)
	}
	return failai
}

func RedisInit() {
        redisdb = redis.NewClient(&redis.Options{Addr: "localhost:6379", Password: "", DB: 0, ReadTimeout: time.Hour})
}

func main() {
	fmt.Printf("Telegram CCTV interface v%s build: %s (%s). Copyright (c) 2020-2021 e1z0\n", Version, Build, Commit)
	daemonize := flag.Bool("d", false, "Daemonize the client")
	flag.Parse()
	if *daemonize {
		fmt.Printf("Program have been run in daemonize mode...\n")
	}
	// declare telegram
	LoadSnapshot()
        RedisInit()
	bot, _ = tgbotapi.NewBotAPI(settings.TelegramBotToken)
	bot.Debug = false
	fmt.Printf("Authorized on account %s\n", bot.Self.UserName)
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates, err := bot.GetUpdatesChan(u)
	go func() {
		for update := range updates {
			if update.Message == nil && update.CallbackQuery == nil {
				continue
			}

			if update.CallbackQuery != nil {
				if strings.HasPrefix(update.CallbackQuery.Data, "timelapse#") {
					data := strings.Split(update.CallbackQuery.Data, "#")
					fmt.Printf("Callback partially worked! Date: %s\n", data[1])
					Failai := QueryTimeEvents(data[1])
					if len(Failai) > 0 {
						if len(Failai) > 1 {
							fmt.Printf("Multiple post to telegram ->\n")
							go TGUploadMultiImage(update.CallbackQuery.Message.Chat.ID, update.CallbackQuery.Message.MessageID, Failai)
						} else {
							fmt.Printf("Single post to telegram ->\n")
							media := struct {
								Type  string `json:"type"`
								Media string `json:"media"`
							}{Type: "video", Media: "attach://" + data[1]}
							mediaJson, _ := json.Marshal(media)
							imageFile, err := ioutil.ReadFile(Failai[0].Name)
							if err != nil {
								fmt.Printf("Unable to read file: %v\nTelegram single post failed!\n", err)
							}
							if err == nil {
								TestFile := tgbotapi.FileBytes{Name: data[1], Bytes: imageFile}
								_, err := bot.UploadFile(
									"editMessageMedia", map[string]string{
										"chat_id":    strconv.FormatInt(update.CallbackQuery.Message.Chat.ID, 10),
										"message_id": strconv.Itoa(update.CallbackQuery.Message.MessageID),
										"media":      string(mediaJson),
									},
									TestFile.Name,
									TestFile,
								)
								if err != nil {
									fmt.Printf("Single post to telegram was somehow failed wit err: %s\n", err)
								} else {
									fmt.Printf("Single post to telegram was a success!\n")
								}
							}

						}
					} else {
						fmt.Printf("We do not have anything to post to telegram\n")
					}
				}
			}
		}
	}()

	//create a ClientOptions
	imgkanalas := "frigate/+/+/snapshot"
	detectkanalas := "frigate/+/+"
	kanalai := make(map[string]byte)
	kanalai[imgkanalas] = 0
	kanalai[detectkanalas] = 0
	opts := mqtt.NewClientOptions().
		AddBroker(settings.Mqttbroker).
		SetClientID("telegram-cctv").
		SetDefaultPublishHandler(messageHandler).
		SetConnectionLostHandler(connLostHandler)

	opts.OnConnect = func(c mqtt.Client) {
		fmt.Printf("Client connected, subscribing to: %v\n", kanalai)
		if token := c.SubscribeMultiple(kanalai, nil); token.Wait() && token.Error() != nil {
			fmt.Println(token.Error())
			os.Exit(1)
		}
	}

	//create and start a client using the above ClientOptions
	c := mqtt.NewClient(opts)
	if token := c.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}

	if *daemonize == false {
		// keyboard interception and control
		err = term.Init()
		if err != nil {
			panic(err)
		}
		defer term.Close()
		fmt.Println("Enter p key to see the stats or q key to quit")
	keyPressListenerLoop:
		for {
			switch ev := term.PollEvent(); ev.Type {
			case term.EventKey:
				if ev.Ch == 'p' {
					DebugShow()
				} else if ev.Ch == 'q' {
					Finish()
					break keyPressListenerLoop
				} else if ev.Ch == 'r' {
					Resend()
				}
			case term.EventError:
				panic(ev.Err)
			}
		}
		//reset()
		os.Exit(0)
	}

	for {
		//Lazy...
		time.Sleep(500 * time.Millisecond)
	}
}
