// Package scheduler memicu pengingat harian pada jam yang dikonfigurasi.
package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/robfig/cron/v3"
)

// Start menjadwalkan remind pada setiap jam "HH:MM" di zona loc, berhenti saat ctx selesai.
func Start(ctx context.Context, loc *time.Location, times []string, remind func(context.Context)) error {
	c := cron.New(cron.WithLocation(loc))
	for _, t := range times {
		hm, err := time.Parse("15:04", t)
		if err != nil {
			return err
		}
		spec := fmt.Sprintf("%d %d * * *", hm.Minute(), hm.Hour())
		if _, err := c.AddFunc(spec, func() {
			log.Printf("pengingat %s dipicu", t)
			remind(ctx)
		}); err != nil {
			return err
		}
	}
	c.Start()
	go func() {
		<-ctx.Done()
		<-c.Stop().Done()
	}()
	return nil
}
