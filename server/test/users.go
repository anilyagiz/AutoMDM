package test

import (
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/ptr"
)

var (
	GoodPassword  = "password123#"
	GoodPassword2 = "password123!"
	UserNoRoles   = &automdm.User{
		ID: 1,
	}
	UserAdmin = &automdm.User{
		ID:         2,
		GlobalRole: ptr.String(automdm.RoleAdmin),
		Email:      "useradmin@example.com",
	}
	UserMaintainer = &automdm.User{
		ID:         3,
		GlobalRole: ptr.String(automdm.RoleMaintainer),
	}
	UserObserver = &automdm.User{
		ID:         4,
		GlobalRole: ptr.String(automdm.RoleObserver),
	}
	UserTeamAdminTeam1 = &automdm.User{
		ID: 5,
		Teams: []automdm.UserTeam{
			{
				Team: automdm.Team{ID: 1},
				Role: automdm.RoleAdmin,
			},
		},
	}
	UserTeamAdminTeam2 = &automdm.User{
		ID: 6,
		Teams: []automdm.UserTeam{
			{
				Team: automdm.Team{ID: 2},
				Role: automdm.RoleAdmin,
			},
		},
	}
	UserTeamMaintainerTeam1 = &automdm.User{
		ID: 7,
		Teams: []automdm.UserTeam{
			{
				Team: automdm.Team{ID: 1},
				Role: automdm.RoleMaintainer,
			},
		},
	}
	UserTeamMaintainerTeam2 = &automdm.User{
		ID: 8,
		Teams: []automdm.UserTeam{
			{
				Team: automdm.Team{ID: 2},
				Role: automdm.RoleMaintainer,
			},
		},
	}
	UserTeamObserverTeam1 = &automdm.User{
		ID: 9,
		Teams: []automdm.UserTeam{
			{
				Team: automdm.Team{ID: 1},
				Role: automdm.RoleObserver,
			},
		},
	}
	UserTeamObserverTeam2 = &automdm.User{
		ID: 10,
		Teams: []automdm.UserTeam{
			{
				Team: automdm.Team{ID: 2},
				Role: automdm.RoleObserver,
			},
		},
	}
	UserTeamObserverTeam1TeamAdminTeam2 = &automdm.User{
		ID: 11,
		Teams: []automdm.UserTeam{
			{
				Team: automdm.Team{ID: 1},
				Role: automdm.RoleObserver,
			},
			{
				Team: automdm.Team{ID: 2},
				Role: automdm.RoleAdmin,
			},
		},
	}
	UserObserverPlus = &automdm.User{
		ID:         12,
		GlobalRole: ptr.String(automdm.RoleObserverPlus),
	}
	UserTeamObserverPlusTeam1 = &automdm.User{
		ID: 13,
		Teams: []automdm.UserTeam{
			{
				Team: automdm.Team{ID: 1},
				Role: automdm.RoleObserverPlus,
			},
		},
	}
	UserTeamObserverPlusTeam2 = &automdm.User{
		ID: 14,
		Teams: []automdm.UserTeam{
			{
				Team: automdm.Team{ID: 2},
				Role: automdm.RoleObserverPlus,
			},
		},
	}
	UserGitOps = &automdm.User{
		ID:         15,
		GlobalRole: ptr.String(automdm.RoleGitOps),
	}
	UserTeamGitOpsTeam1 = &automdm.User{
		ID: 16,
		Teams: []automdm.UserTeam{
			{
				Team: automdm.Team{ID: 1},
				Role: automdm.RoleGitOps,
			},
		},
	}
	UserTeamGitOpsTeam2 = &automdm.User{
		ID: 17,
		Teams: []automdm.UserTeam{
			{
				Team: automdm.Team{ID: 2},
				Role: automdm.RoleGitOps,
			},
		},
	}
	UserTechnician = &automdm.User{
		ID:         18,
		GlobalRole: ptr.String(automdm.RoleTechnician),
	}
	UserTeamTechnicianTeam1 = &automdm.User{
		ID: 19,
		Teams: []automdm.UserTeam{
			{
				Team: automdm.Team{ID: 1},
				Role: automdm.RoleTechnician,
			},
		},
	}
	UserTeamTechnicianTeam2 = &automdm.User{
		ID: 20,
		Teams: []automdm.UserTeam{
			{
				Team: automdm.Team{ID: 2},
				Role: automdm.RoleTechnician,
			},
		},
	}
)
