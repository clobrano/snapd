// -*- Mode: Go; indent-tabs-mode: t -*-

/*
 * Copyright (C) 2017 Canonical Ltd
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License version 3 as
 * published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

package ctlcmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/snapcore/snapd/i18n"
	"github.com/snapcore/snapd/overlord/configstate"
	"github.com/snapcore/snapd/overlord/hookstate"
	"github.com/snapcore/snapd/overlord/servicestate"
	"github.com/snapcore/snapd/overlord/snapstate"
	"github.com/snapcore/snapd/overlord/state"
	"github.com/snapcore/snapd/snap"
)

func getServiceInfos(st *state.State, snapName string, serviceNames []string) ([]*snap.AppInfo, error) {
	st.Lock()
	defer st.Unlock()

	var snapst snapstate.SnapState
	if err := snapstate.Get(st, snapName, &snapst); err != nil {
		return nil, err
	}

	info, err := snapst.CurrentInfo()
	if err != nil {
		return nil, err
	}

	var svcs []*snap.AppInfo
	for _, svcName := range serviceNames {
		if svcName == snapName {
			// all the services
			return info.Services(), nil
		}
		if !strings.HasPrefix(svcName, snapName+".") {
			return nil, fmt.Errorf(i18n.G("unknown service: %q"), svcName)
		}
		// this doesn't support service aliases
		app, ok := info.Apps[svcName[1+len(snapName):]]
		if !(ok && app.IsService()) {
			return nil, fmt.Errorf(i18n.G("unknown service: %q"), svcName)
		}
		svcs = append(svcs, app)
	}

	return svcs, nil
}

var servicestateControl = servicestate.Control

func queueCommand(context *hookstate.Context, ts *state.TaskSet) {
	// queue command task after all existing tasks of the hook's change
	st := context.State()
	st.Lock()
	defer st.Unlock()

	task, ok := context.Task()
	if !ok {
		panic("attempted to queue command with ephemeral context")
	}
	change := task.Change()
	tasks := change.Tasks()
	ts.WaitAll(state.NewTaskSet(tasks...))
	change.AddAll(ts)
}

func runServiceCommand(context *hookstate.Context, inst *servicestate.Instruction, serviceNames []string) error {
	if context == nil {
		return fmt.Errorf(i18n.G("cannot %s without a context"), inst.Action)
	}

	st := context.State()
	appInfos, err := getServiceInfos(st, context.SnapName(), serviceNames)
	if err != nil {
		return err
	}

	ts, err := servicestateControl(st, appInfos, inst)
	if err != nil {
		return err
	}

	if !context.IsEphemeral() && context.HookName() == "configure" {
		queueCommand(context, ts)
		return nil
	}

	st.Lock()
	chg := st.NewChange("service-control", fmt.Sprintf("Running service command for snap %q", context.SnapName()))
	chg.AddAll(ts)
	st.EnsureBefore(0)
	st.Unlock()

	select {
	case <-chg.Ready():
		st.Lock()
		defer st.Unlock()
		return chg.Err()
	case <-time.After(configstate.ConfigureHookTimeout() / 2):
		return fmt.Errorf("%s command is taking too long", inst.Action)
	}
}
