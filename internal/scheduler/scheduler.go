package scheduler

import (
	"context"
	"log"
	"strconv"
	"time"

	"cert-live/internal/model"
	"cert-live/internal/notify"
	"cert-live/internal/probe"
	"cert-live/internal/store"
)

type Scheduler struct {
	st *store.Store
}

func New(st *store.Store) *Scheduler {
	return &Scheduler{st: st}
}

// Run blocks until ctx is cancelled, scanning all domains on a configurable interval.
func (s *Scheduler) Run(ctx context.Context) {
	// Run an initial sweep shortly after boot instead of waiting a full interval.
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
			s.CheckAll()
		}
	}()

	for {
		settings := readSettings(s.st)
		interval := time.Duration(settings.CheckIntervalMin) * time.Minute
		if interval <= 0 {
			interval = 6 * time.Hour
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
			s.CheckAll()
		}
	}
}

// CheckAll probes every domain once, records results, and fires consolidated alerts.
func (s *Scheduler) CheckAll() {
	ids, err := s.st.ListAllDomainIDs()
	if err != nil {
		log.Printf("scheduler: list domains: %v", err)
		return
	}
	var alerts []notify.Alert
	settings := readSettings(s.st)
	tiers := settings.Tiers()
	for _, id := range ids {
		alerts = append(alerts, s.checkOne(id, tiers)...)
	}
	if len(alerts) == 0 {
		return
	}
	ch := notify.Channel{
		FeishuWebhook: settings.FeishuWebhook,
		FeishuSecret:  settings.FeishuSecret,
		WeComWebhook:  settings.WeComWebhook,
	}
	if errs := ch.Send(alerts); len(errs) > 0 {
		for _, e := range errs {
			log.Printf("notify: %v", e)
		}
	}
}

// CheckOne probes a single domain and returns any alerts it produced.
func (s *Scheduler) CheckOne(domainID int64) []notify.Alert {
	return s.checkOne(domainID, readSettings(s.st).Tiers())
}

func (s *Scheduler) checkOne(domainID int64, tiers []int) []notify.Alert {
	dom, err := s.st.GetDomain(domainID)
	if err != nil || dom == nil {
		log.Printf("scheduler: get domain %d: %v", domainID, err)
		return nil
	}
	now := time.Now().Unix()
	res, err := probe.Probe(dom.Host, dom.Port)
	if err != nil {
		// Unreachable / decommissioned host: record the failure silently.
		// Per spec we do NOT raise expiry alerts for offline domains.
		rec := *dom
		rec.LastChecked = now
		rec.LastError = err.Error()
		if e := s.st.UpsertCertRecord(rec); e != nil {
			log.Printf("scheduler: upsert error for %s: %v", dom.Host, e)
		}
		return nil
	}

	rec := *dom
	rec.Subject = res.Subject
	rec.Issuer = res.Issuer
	rec.SANs = res.SANs
	rec.SerialNumber = res.SerialNumber
	rec.NotBefore = res.NotBefore.Unix()
	rec.NotAfter = res.NotAfter.Unix()
	rec.IsWildcard = res.IsWildcard
	rec.DaysRemaining = res.DaysRemaining
	rec.LastChecked = now
	if e := s.st.UpsertCertRecord(rec); e != nil {
		log.Printf("scheduler: upsert for %s: %v", dom.Host, e)
		return nil
	}

	// Only the live cert can trigger alerts, and we de-dupe per (cert, tier),
	// so a replaced cert with a fresh serial resets the alert history naturally.
	var alerts []notify.Alert
	for _, tier := range tiers {
		threshold := tier
		alreadyAlerted, err := s.st.HasAlerted(domainID, res.SerialNumber, threshold)
		if err != nil {
			log.Printf("scheduler: has-alerted check: %v", err)
			continue
		}
		if res.DaysRemaining > threshold || alreadyAlerted {
			continue
		}
		if e := s.st.RecordAlert(domainID, res.SerialNumber, threshold); e != nil {
			log.Printf("scheduler: record alert: %v", e)
			continue
		}
		alerts = append(alerts, notify.Alert{
			Host:      dom.Host,
			DaysLeft:  res.DaysRemaining,
			NotAfter:  res.NotAfter,
			Subject:   res.Subject,
			Issuer:    res.Issuer,
			Tier:      threshold,
			IsExpired: res.DaysRemaining <= 0,
		})
	}
	return alerts
}

func readSettings(st *store.Store) model.Settings {
	m, err := st.GetAll()
	if err != nil {
		return model.DefaultSettings()
	}
	s := model.DefaultSettings()
	if v, ok := m["feishu_webhook"]; ok {
		s.FeishuWebhook = v
	}
	if v, ok := m["feishu_secret"]; ok {
		s.FeishuSecret = v
	}
	if v, ok := m["wecom_webhook"]; ok {
		s.WeComWebhook = v
	}
	if v, ok := m["alert_tiers"]; ok {
		s.AlertTiersJSON = v
	}
	if v, ok := m["check_interval"]; ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			s.CheckIntervalMin = n
		}
	}
	return s
}