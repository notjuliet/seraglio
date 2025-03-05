package main

import (
	"fmt"
	"log"
	"os"

	_ "github.com/joho/godotenv/autoload"
	"github.com/notjuliet/seraglio"
	"github.com/urfave/cli/v2"
)

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
			if appid == "" {
				return fmt.Errorf("Application ID is required")
			}
			bot, err := seraglio.NewBot(token, appid)
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
