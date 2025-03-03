package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
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
				Description: "The message is only visibile to you",
				Type:        discordgo.ApplicationCommandOptionBoolean,
				Required:    false,
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
				return fmt.Errorf("Application ID is required")
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
		if data.Name == "leaderboard" {
			b.handleLeaderboard(s, i, parseOptions(data.Options))
		}
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

func (b *Bot) handleLeaderboard(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	opts optionMap,
) {
	var channel *discordgo.ApplicationCommandInteractionDataOption
	if c, ok := opts["channel"]; ok {
		channel = c
	}

	var flags discordgo.MessageFlags
	if ephemeral, ok := opts["ephemeral"]; ok {
		if ephemeral.BoolValue() {
			flags = discordgo.MessageFlagsEphemeral
		}
	}

	var q string
	var args []any
	if channel != nil {
		q = "SELECT user_id, end_time, start_time FROM user_sessions WHERE guild_id = ? AND channel_id = ?"
		args = []any{i.GuildID, channel.ChannelValue(nil).ID}
	} else {
		q = "SELECT user_id, end_time, start_time FROM user_sessions WHERE guild_id = ?"
		args = []any{i.GuildID}
	}

	var rows []struct {
		UserID    string
		StartTime time.Time
		EndTime   *time.Time
	}
	if err := b.db.Raw(q, args...).Scan(&rows).Error; err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("Error fetching leaderboard: %v", err),
			},
		})
		return
	}

	usersTotal := map[string]time.Duration{}
	for _, row := range rows {
		if row.EndTime == nil {
			t := time.Now()
			row.EndTime = &t
		}
		usersTotal[row.UserID] += row.EndTime.Sub(row.StartTime)
	}

	type kv struct {
		User  string
		Total time.Duration
	}

	var sd []kv
	for k, v := range usersTotal {
		sd = append(sd, kv{k, v})
	}

	sort.Slice(sd, func(i, j int) bool {
		return sd[i].Total > sd[j].Total
	})

	var content string
	for n, kv := range sd {
		u, err := s.User(kv.User)
		if err != nil {
			log.Printf("Error fetching user: %v", err)
			continue
		}

		days := kv.Total / (24 * time.Hour)
		kv.Total -= days * 24 * time.Hour

		hours := kv.Total / time.Hour
		kv.Total -= hours * time.Hour

		minutes := kv.Total / time.Minute
		kv.Total -= minutes * time.Minute

		seconds := kv.Total / time.Second

		var result string

		if days > 0 {
			result += fmt.Sprintf("%dd ", days)
		}
		if hours > 0 {
			result += fmt.Sprintf("%dh ", hours)
		}
		if minutes > 0 {
			result += fmt.Sprintf("%dm ", minutes)
		}
		if seconds > 0 {
			result += fmt.Sprintf("%ds ", seconds)
		}

		if len(result) > 0 {
			result = result[:len(result)-1]
		} else {
			result = "0s"
		}

		content += fmt.Sprintf(
			"**%d.** %s: %s\n",
			n+1,
			u.Mention(),
			result,
		)
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: flags,
			Embeds: []*discordgo.MessageEmbed{
				{
					Title: "Leaderboard",
					Fields: []*discordgo.MessageEmbedField{
						{
							Name:  "Time Spent in VC",
							Value: content,
						},
					},
				},
			},
		}})
}
