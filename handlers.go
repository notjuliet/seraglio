package seraglio

import (
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/bwmarrin/discordgo"
)

type optionMap = map[string]*discordgo.ApplicationCommandInteractionDataOption

func parseOptions(options []*discordgo.ApplicationCommandInteractionDataOption) (om optionMap) {
	om = make(optionMap)
	for _, opt := range options {
		om[opt.Name] = opt
	}
	return
}

func (b *Bot) GuildCreate(s *discordgo.Session, gc *discordgo.GuildCreate) {
	for _, vs := range gc.VoiceStates {
		b.createUserSession(vs.UserID, gc.ID, vs.ChannelID)
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

		b.createUserSession(vs.UserID, vs.GuildID, vs.ChannelID)
	} else {
		q := "UPDATE user_sessions SET end_time = ? WHERE user_id = ? AND end_time IS NULL"
		if err := b.db.Exec(q, time.Now(), vs.UserID).Error; err != nil {
			log.Printf("Error updating user session: %v", err)
		}
	}
}

func (b *Bot) createUserSession(userID, guildID, channelID string) {
	s := &UserSession{
		SessionID: fmt.Sprintf("%s-%s-%s", userID, channelID, time.Now().Format(time.RFC3339)),
		UserID:    userID,
		GuildID:   guildID,
		ChannelID: channelID,
		StartTime: time.Now(),
	}
	b.db.Create(s)
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

		content += fmt.Sprintf("**%d.** %s: %s\n", n+1, u.Mention(), result)
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
		},
	})
}
