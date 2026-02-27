package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/o-ermakov/cnpg-pg-doorman/cmd/plugin"
)

func main() {
	rootCmd := &cobra.Command{
		Use: "pg-doorman-plugin",
	}

	rootCmd.AddCommand(plugin.NewCmd())

	if err := rootCmd.ExecuteContext(ctrl.SetupSignalHandler()); err != nil {
		if !errors.Is(err, context.Canceled) {
			fmt.Println(err)
			os.Exit(1)
		}
	}
}
