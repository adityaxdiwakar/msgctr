package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/bwmarrin/discordgo"
	"github.com/go-redis/redis/v8"
)

type tomlConfig struct {
	Redis struct {
		Address  string
		Password string
		DB       int
	}
	Discord struct {
		Token   string
		Webhook string
	}
}

var ctx = context.Background()
var sinceCounter = int64(0)
var prevTimestamp time.Time
var conf tomlConfig
var rdb *redis.Client
var httpClient *http.Client

func init() {
	if _, err := toml.DecodeFile("config.toml", &conf); err != nil {
		log.Fatalf("error: could not parse configuration: %v\n", err)
	}

	rdb = redis.NewClient(&redis.Options{
		Addr:     conf.Redis.Address,
		Password: conf.Redis.Password,
		DB:       conf.Redis.DB,
	})

	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		log.Fatal("Could not make connection with Redis")
	}

	// init the clock
	prevTimestamp = time.Now()

	// init http
	httpClient = &http.Client{}
}

func main() {
	dg, err := discordgo.New(fmt.Sprintf("%s", conf.Discord.Token))
	if err != nil {
		fmt.Println("Error creating Discord Session due to:", err)
		return
	}

	err = dg.Open()
	if err != nil {
		fmt.Println("Error opening connection:", err)
		return
	}

	fmt.Println("Succesfully connected, adding handlers!")

	dg.AddHandler(newMessage)

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc
	fmt.Println("Interrupt received, terminating msgctr")
}

type WebhookRequest struct {
	Content string                    `json:"content"`
	Embeds  []*discordgo.MessageEmbed `json:"embeds"`
}

func newMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == "192696739981950976" {
		rdb.Incr(ctx, "msgctr.author.sent")
		rdb.IncrBy(ctx, "msgctr.author.received", sinceCounter)
		rdb.IncrBy(ctx, "msgctr.author.sent.words", int64(len(strings.Split(m.Content, " "))))
		rdb.IncrBy(ctx, "msgctr.author.sent.chars", int64(len(m.Content)))

		// check for day wrap
		if prevTimestamp.Day() != time.Now().Day() || m.Content == "d38f97626a85c40844777e2924df87bc" {
			// send report
			sent, sErr := rdb.Get(ctx, "msgctr.author.sent").Result()
			sentWords, swErr := rdb.Get(ctx, "msgctr.author.sent.words").Result()
			sentChars, scErr := rdb.Get(ctx, "msgctr.author.sent.chars").Result()
			received, rErr := rdb.Get(ctx, "msgctr.author.sent").Result()

			if sErr != nil || rErr != nil || swErr != nil || scErr != nil {
				log.Printf("Ruh-roh! Something went wrong with getting the message count.\n")
			}

			// generate report
			embed := WebhookRequest{
				Content: "<@192696739981950976> A new report is ready!",
				Embeds: []*discordgo.MessageEmbed{
					{
						Title:       "Message Report for Yesterday",
						Description: "Message report for the previous day",
						Fields: []*discordgo.MessageEmbedField{
							{
								Name:  "Sent Messages",
								Value: sent,
							},
							{
								Name:  "Content Sent",
								Value: fmt.Sprintf("%sc in %sw", sentChars, sentWords),
							},
							{
								Name:  "Received Messages",
								Value: received,
							},
						},
					},
				},
			}

			jsonValue, _ := json.Marshal(embed)
			_, err := httpClient.Post(conf.Discord.Webhook, "application/json", bytes.NewBuffer(jsonValue))
			if err != nil {
				log.Printf("Something went wrong sending the daily report!")
			}

			// reset values
			if m.Content != "d38f97626a85c40844777e2924df87bc" {
				rdb.Set(ctx, "msgctr.author.sent", 0, 0)
				rdb.Set(ctx, "msgctr.author.sent.words", 0, 0)
				rdb.Set(ctx, "msgctr.author.sent.chars", 0, 0)
				rdb.Set(ctx, "msgctr.author.received", 0, 0)
			}
		}

		return
	}

	sinceCounter += 1
}
