package seraglio

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type Bot struct {
	db      *gorm.DB
	session *discordgo.Session
}

type UserSession struct {
	SessionID string `gorm:"primaryKey"`
	UserID    string `gorm:"index"`
	GuildID   string `gorm:"index"`
	ChannelID string `gorm:"index"`
	StartTime time.Time
	EndTime   *time.Time
}

var commands = []*discordgo.ApplicationCommand{
	{
		Name:        "leaderboard",
		Description: "VC activity leaderboard",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "channel",
				Description: "Name of the channel",
				Type:        discordgo.ApplicationCommandOptionChannel,
				ChannelTypes: []discordgo.ChannelType{
					discordgo.ChannelTypeGuildVoice,
				},
				Required: false,
			},
			{
				Name:        "ephemeral",
				Description: "The message is only visible to you",
				Type:        discordgo.ApplicationCommandOptionBoolean,
				Required:    false,
			},
		},
	},
}

func NewBot(token string, appid string) (*Bot, error) {
	db, err := gorm.Open(sqlite.Open("seraglio.db"), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("Error opening database: %v", err)
	}

	db.AutoMigrate(&UserSession{})

	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("Error creating Discord session: %v", err)
	}

	b := &Bot{db, dg}
	dg.AddHandler(b.userJoin)
	dg.AddHandler(b.GuildCreate)
	dg.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.Type != discordgo.InteractionApplicationCommand {
			return
		}

		data := i.ApplicationCommandData()
		if data.Name == "leaderboard" {
			b.handleLeaderboard(s, i, parseOptions(data.Options))
		}
	})

	_, err = dg.ApplicationCommandBulkOverwrite(appid, "", commands)
	if err != nil {
		return nil, fmt.Errorf("could not register commands: %v", err)
	}

	dg.Identify.Intents = discordgo.IntentGuildVoiceStates | discordgo.IntentGuilds | discordgo.IntentGuildMembers

	err = dg.Open()
	if err != nil {
		return nil, fmt.Errorf("Error opening connection: %v", err)
	}

	return b, nil
}

func (b *Bot) Run() {
	fmt.Println("Bot is now running. Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	b.session.Close()
	if err := b.db.Exec("UPDATE user_sessions SET end_time = ? WHERE end_time IS NULL", time.Now()).Error; err != nil {
		log.Printf("Error updating user sessions: %v", err)
	}
}
