package singleprocess

import (
	"context"

	"github.com/hashicorp/go-hclog"
	empty "google.golang.org/protobuf/types/known/emptypb"

	pb "github.com/hashicorp/waypoint/pkg/server/gen"
	"github.com/hashicorp/waypoint/pkg/server/hcerr"
	serverptypes "github.com/hashicorp/waypoint/pkg/server/ptypes"
)

func (s *Service) UpsertProject(
	ctx context.Context,
	req *pb.UpsertProjectRequest,
) (*pb.UpsertProjectResponse, error) {
	if err := serverptypes.ValidateUpsertProjectRequest(req); err != nil {
		return nil, err
	}

	result := req.Project
	if err := s.state(ctx).ProjectPut(result); err != nil {
		return nil, err
	}

	if projectNeedsRemoteInit(result) {
		// The project is connected to a data source but doesn’t use
		// automatic polling, so let’s queue some remote init operations
		// to ensure the application list is populated.

		// TODO(jgwhite): only queue init ops if the relevant fields have *changed*

		err := queueInitOps(s, ctx, result)

		if err != nil {
			// An error here indicates a failure to enqueue an
			// InitOp, not a failure during the operation itself,
			// which happen out-of-band.
			return nil, hcerr.Externalize(hclog.FromContext(ctx), err, "failed queueing init job")
		}
	}

	return &pb.UpsertProjectResponse{Project: result}, nil
}

func (s *Service) GetProject(
	ctx context.Context,
	req *pb.GetProjectRequest,
) (*pb.GetProjectResponse, error) {
	if err := serverptypes.ValidateGetProjectRequest(req); err != nil {
		return nil, err
	}

	result, err := s.state(ctx).ProjectGet(req.Project)
	if err != nil {
		return nil, err
	}

	// Get all the workspaces that this project is part of
	workspaces, err := s.state(ctx).ProjectListWorkspaces(req.Project)
	if err != nil {
		return nil, err
	}

	return &pb.GetProjectResponse{
		Project:    result,
		Workspaces: workspaces,
	}, nil
}

func (s *Service) ListProjects(
	ctx context.Context,
	req *empty.Empty,
) (*pb.ListProjectsResponse, error) {
	result, err := s.state(ctx).ProjectList()
	if err != nil {
		return nil, err
	}

	return &pb.ListProjectsResponse{Projects: result}, nil
}

func (s *Service) GetApplication(
	ctx context.Context,
	req *pb.GetApplicationRequest,
) (*pb.GetApplicationResponse, error) {
	if err := serverptypes.ValidateGetApplicationRequest(req); err != nil {
		return nil, err
	}

	result, err := s.state(ctx).AppGet(req.Application)
	if err != nil {
		return nil, err
	}

	return &pb.GetApplicationResponse{
		Application: result,
	}, nil
}

func (s *Service) UpsertApplication(
	ctx context.Context,
	req *pb.UpsertApplicationRequest,
) (*pb.UpsertApplicationResponse, error) {
	if err := serverptypes.ValidateUpsertApplicationRequest(req); err != nil {
		return nil, err
	}

	// Get the project
	praw, err := s.state(ctx).ProjectGet(req.Project)
	if err != nil {
		return nil, err
	}

	var app *pb.Application

	// If the project has the application already then we're done.
	p := serverptypes.Project{Project: praw}
	if idx := p.App(req.Name); idx >= 0 {
		app = p.Applications[idx]
	} else {
		app = &pb.Application{
			Project: req.Project,
			Name:    req.Name,
		}
	}

	app.FileChangeSignal = req.FileChangeSignal

	app, err = s.state(ctx).AppPut(app)
	if err != nil {
		return nil, err
	}

	return &pb.UpsertApplicationResponse{Application: app}, nil
}

func queueInitOps(s *Service, ctx context.Context, project *pb.Project) error {
	workspaces, err := s.state(ctx).WorkspaceList()
	if err != nil {
		return err
	}

	if len(workspaces) == 0 {
		workspaces = append(workspaces, &pb.Workspace{Name: "default"})
	}

	for _, workspace := range workspaces {
		_, err := s.QueueJob(ctx, &pb.QueueJobRequest{
			Job: &pb.Job{
				Application: &pb.Ref_Application{
					Project: project.Name,
				},
				Workspace: &pb.Ref_Workspace{
					Workspace: workspace.Name,
				},
				Operation: &pb.Job_Init{},
				TargetRunner: &pb.Ref_Runner{
					Target: &pb.Ref_Runner_Any{},
				},
			},
		})

		if err != nil {
			return err
		}
	}

	return nil
}

func projectNeedsRemoteInit(project *pb.Project) bool {
	if project.DataSource == nil {
		return false
	}

	if project.DataSource.GetGit() == nil {
		return false
	}

	if project.DataSourcePoll != nil && project.DataSourcePoll.Enabled {
		return false
	}

	return true
}
