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
func Initialize(c context.Context, DBFunc func() *gorp.DbMap) {
	nbUsers := prometheus.NewSummaryVec(prometheus.SummaryOpts{Name: "nb_users", Help: "metrics nb_users"}, []string{"service"})
	nbApplications := prometheus.NewSummaryVec(prometheus.SummaryOpts{Name: "nb_applications", Help: "metrics nb_applications"}, []string{"service"})
	nbProjects := prometheus.NewSummaryVec(prometheus.SummaryOpts{Name: "nb_projects", Help: "metrics nb_projects"}, []string{"service"})
	nbGroups := prometheus.NewSummaryVec(prometheus.SummaryOpts{Name: "nb_groups", Help: "metrics nb_groups"}, []string{"service"})
	nbPipelines := prometheus.NewSummaryVec(prometheus.SummaryOpts{Name: "nb_pipelines", Help: "metrics nb_pipelines"}, []string{"service"})
	nbWorkflows := prometheus.NewSummaryVec(prometheus.SummaryOpts{Name: "nb_workflows", Help: "metrics nb_workflows"}, []string{"service"})
	nbArtifacts := prometheus.NewSummaryVec(prometheus.SummaryOpts{Name: "nb_artifacts", Help: "metrics nb_artifacts"}, []string{"service"})
	nbWorkerModels := prometheus.NewSummaryVec(prometheus.SummaryOpts{Name: "nb_worker_models", Help: "metrics nb_worker_models"}, []string{"service"})

	registerer.MustRegister(nbUsers)
	registerer.MustRegister(nbApplications)
	registerer.MustRegister(nbProjects)
	registerer.MustRegister(nbGroups)
	registerer.MustRegister(nbPipelines)
	registerer.MustRegister(nbWorkflows)
	registerer.MustRegister(nbArtifacts)
	registerer.MustRegister(nbWorkerModels)

	tick := time.NewTicker(5 * time.Second).C

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
				count(DBFunc(), "artifacts", nbArtifacts)
				count(DBFunc(), "worker_model", nbWorkerModels)
			}
		}
	}(c, DBFunc)
}

func count(db *gorp.DbMap, table string, v *prometheus.SummaryVec) {
	if db == nil {
		return
	}
	var n int64
	if err := db.QueryRow(fmt.Sprintf(`SELECT COUNT(1) FROM "%s"`, table)).Scan(&n); err != nil {
		log.Warning("metrics>Errors while fetching count users")
		return
	}
	v.WithLabelValues("uniform").Observe(float64(n))
}

// GetGatherer returns CDS API gatherer
func GetGatherer() prometheus.Gatherer {
	return gatherer
}
