package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	_ "github.com/joho/godotenv/autoload"
	"github.com/urfave/cli/v2"
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
		Name:        "timespent",
		Description: "Get the time spent of a user in VC",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "User to get time spent for",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "channel",
				Description: "Name of the channel",
				Type:        discordgo.ApplicationCommandOptionChannel,
				ChannelTypes: []discordgo.ChannelType{
					discordgo.ChannelTypeGuildVoice,
				},
				Required: false,
			},
		},
	},
}

type optionMap = map[string]*discordgo.ApplicationCommandInteractionDataOption

func parseOptions(options []*discordgo.ApplicationCommandInteractionDataOption) (om optionMap) {
	om = make(optionMap)
	for _, opt := range options {
		om[opt.Name] = opt
	}
	return
}

func main() {
	app := &cli.App{
		Name: "Seraglio",
		Action: func(cmd *cli.Context) error {
			fmt.Println("Initializing Seraglio...")
			token := cmd.String("discord-token")
			if token == "" {
				return fmt.Errorf("Discord token is required")
			}
			appid := cmd.String("app-id")
			if token == "" {
				return fmt.Errorf("Discord token is required")
			}
			bot, err := NewBot(token, appid)
			if err != nil {
				return fmt.Errorf("Error initializing bot: %v", err)
			}

			bot.Run()

			return nil
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "discord-token",
				EnvVars: []string{"DISCORD_TOKEN"},
			},
			&cli.StringFlag{
				Name:    "app-id",
				EnvVars: []string{"APP_ID"},
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
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
		if data.Name != "timespent" {
			return
		}

		b.handleTimespent(s, i, parseOptions(data.Options))
	})

	_, err = dg.ApplicationCommandBulkOverwrite(appid, "", commands)
	if err != nil {
		log.Fatalf("could not register commands: %s", err)
	}

	dg.Identify.Intents = discordgo.IntentGuildVoiceStates | discordgo.IntentGuilds | discordgo.IntentGuildMembers

	err = dg.Open()
	if err != nil {
		return nil, fmt.Errorf("Error opening connection: %v", err)
	}

	return b, nil
}

func (b *Bot) Run() {
	fmt.Println("Bot is now running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	b.session.Close()
	if err := b.db.Exec("UPDATE user_sessions SET end_time = ? WHERE end_time IS NULL", time.Now()).Error; err != nil {
		log.Printf("Error updating user sessions: %v", err)
	}
}

func (b *Bot) GuildCreate(s *discordgo.Session, gc *discordgo.GuildCreate) {
	for _, vs := range gc.VoiceStates {
		s := &UserSession{
			SessionID: fmt.Sprintf(
				"%s-%s-%s",
				vs.UserID,
				vs.ChannelID,
				time.Now().Format(time.RFC3339),
			),
			UserID:    vs.UserID,
			GuildID:   gc.ID,
			ChannelID: vs.ChannelID,
			StartTime: time.Now(),
		}
		b.db.Create(s)
	}
}

func (b *Bot) userJoin(s *discordgo.Session, vs *discordgo.VoiceStateUpdate) {
	if vs.ChannelID != "" {
		var rows []UserSession
		if err := b.db.Raw("SELECT * FROM user_sessions WHERE user_id = ? AND end_time IS NULL", vs.UserID).Scan(&rows).Error; err != nil {
			log.Printf("Error checking for existing user session: %v", err)
			return
		}

		if len(rows) > 0 {
			return
		}

		// User joined a voice channel
		s := &UserSession{
			SessionID: fmt.Sprintf(
				"%s-%s-%s",
				vs.UserID,
				vs.ChannelID,
				time.Now().Format(time.RFC3339),
			),
			UserID:    vs.UserID,
			GuildID:   vs.GuildID,
			ChannelID: vs.ChannelID,
			StartTime: time.Now(),
		}
		b.db.Create(s)
	} else {
		// User left a voice channel
		q := "UPDATE user_sessions SET end_time = ? WHERE user_id = ? AND end_time IS NULL"
		if err := b.db.Exec(q, time.Now(), vs.UserID).Error; err != nil {
			log.Printf("Error updating user session: %v", err)
		}
	}
}

func (b *Bot) handleTimespent(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	opts optionMap,
) {
	usr, ok := opts["user"]
	if !ok {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "User is required",
			},
		})
		return
	}

	usrID := usr.UserValue(nil).ID

	var sessions []UserSession
	if err := b.db.Raw("SELECT * FROM user_sessions WHERE user_id = ? AND guild_id = ?", usrID, i.GuildID).Scan(&sessions).Error; err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("Error fetching user sessions: %v", err),
			},
		})
		return
	}

	var total time.Duration
	for _, s := range sessions {
		if s.EndTime == nil {
			t := time.Now()
			s.EndTime = &t
		}
		total += s.EndTime.Sub(s.StartTime)
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf(
				"User %s has spent %s in voice channels",
				usr.UserValue(nil).Mention(),
				total,
			),
		},
	})
}
