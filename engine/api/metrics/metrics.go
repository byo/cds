package metrics

import (
	"context"
	"fmt"
	"time"

	"github.com/go-gorp/gorp"

	"github.com/ovh/cds/sdk/log"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	registry                         = prometheus.NewRegistry()
	registerer prometheus.Registerer = registry
	gatherer   prometheus.Gatherer   = registry
)

// Initialize initializes metrics
func Initialize(c context.Context, DBFunc func() *gorp.DbMap, instance string) {
	labels := prometheus.Labels{"instance": instance}

	nbUsers := prometheus.NewSummary(prometheus.SummaryOpts{Name: "nb_users", Help: "metrics nb_users", ConstLabels: labels})
	nbApplications := prometheus.NewSummary(prometheus.SummaryOpts{Name: "nb_applications", Help: "metrics nb_applications", ConstLabels: labels})
	nbProjects := prometheus.NewSummary(prometheus.SummaryOpts{Name: "nb_projects", Help: "metrics nb_projects", ConstLabels: labels})
	nbGroups := prometheus.NewSummary(prometheus.SummaryOpts{Name: "nb_groups", Help: "metrics nb_groups", ConstLabels: labels})
	nbPipelines := prometheus.NewSummary(prometheus.SummaryOpts{Name: "nb_pipelines", Help: "metrics nb_pipelines", ConstLabels: labels})
	nbWorkflows := prometheus.NewSummary(prometheus.SummaryOpts{Name: "nb_workflows", Help: "metrics nb_workflows", ConstLabels: labels})
	nbArtifacts := prometheus.NewSummary(prometheus.SummaryOpts{Name: "nb_artifacts", Help: "metrics nb_artifacts", ConstLabels: labels})
	nbWorkerModels := prometheus.NewSummary(prometheus.SummaryOpts{Name: "nb_worker_models", Help: "metrics nb_worker_models", ConstLabels: labels})

	registerer.MustRegister(nbUsers)
	registerer.MustRegister(nbApplications)
	registerer.MustRegister(nbProjects)
	registerer.MustRegister(nbGroups)
	registerer.MustRegister(nbPipelines)
	registerer.MustRegister(nbWorkflows)
	registerer.MustRegister(nbArtifacts)
	registerer.MustRegister(nbWorkerModels)

	tick := time.NewTicker(30 * time.Second).C

	go func(c context.Context, DBFunc func() *gorp.DbMap) {
		for {
			select {
			case <-c.Done():
				if c.Err() != nil {
					log.Error("Exiting metrics.Initialize: %v", c.Err())
					return
				}
			case <-tick:
				count(DBFunc(), "user", nbUsers)
				count(DBFunc(), "application", nbApplications)
				count(DBFunc(), "project", nbProjects)
				count(DBFunc(), "group", nbGroups)
				count(DBFunc(), "pipeline", nbPipelines)
				count(DBFunc(), "workflow", nbWorkflows)
				count(DBFunc(), "artifact", nbArtifacts)
				count(DBFunc(), "worker_model", nbWorkerModels)
			}
		}
	}(c, DBFunc)
}

func count(db *gorp.DbMap, table string, v prometheus.Summary) {
	if db == nil {
		return
	}
	var n int64
	if err := db.QueryRow(fmt.Sprintf(`SELECT COUNT(1) FROM "%s"`, table)).Scan(&n); err != nil {
		log.Warning("metrics>Errors while fetching count %s: %v", table, err)
		return
	}
	v.Observe(float64(n))
}

// GetGatherer returns CDS API gatherer
func GetGatherer() prometheus.Gatherer {
	return gatherer
}
