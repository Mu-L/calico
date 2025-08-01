// Copyright (c) 2016-2017 Tigera, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package calc

import (
	"context"

	"github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/libcalico-go/lib/backend/api"
)

func NewSyncerCallbacksDecoupler() *SyncerCallbacksDecoupler {
	return &SyncerCallbacksDecoupler{
		c:    make(chan interface{}),
		Done: make(chan struct{}),
	}
}

type SyncerCallbacksDecoupler struct {
	c    chan interface{}
	Done chan struct{}
}

func (a *SyncerCallbacksDecoupler) OnStatusUpdated(status api.SyncStatus) {
	a.c <- status
}

func (a *SyncerCallbacksDecoupler) OnUpdates(updates []api.Update) {
	a.c <- updates
}

func (a *SyncerCallbacksDecoupler) SendTo(sink api.SyncerCallbacks) {
	a.SendToContext(context.Background(), sink)
}

func (a *SyncerCallbacksDecoupler) SendToContext(cxt context.Context, sink api.SyncerCallbacks) {
	defer close(a.Done)
	for {
		select {
		case obj := <-a.c:
			switch obj := obj.(type) {
			case api.SyncStatus:
				sink.OnStatusUpdated(obj)
			case []api.Update:
				sink.OnUpdates(obj)
			}
		case <-cxt.Done():
			logrus.WithError(cxt.Err()).Info("Context asked us to stop")
			return
		}
	}
}
