import manifest from 'manifest';
import React from 'react';
import type {Store} from 'redux';

import type {GlobalState} from '@mattermost/types/store';

import {setSiteURL} from './client';
import ConfigSetting from './components/config_setting';
import PluginErrorBoundary from './components/error_boundary';
import OpenCodeBotPost from './components/opencode_bot_post';
import RHSPane from './components/rhs';
import PostEventListener from './post_event_listener';
import {buildPluginWebSocketEventName, handleStreamingPostUpdateEvent} from './streaming';
import type {PluginRegistry} from './types/mattermost-webapp';

const OpenCodeTitle = () => {
    return (
        <span style={{display: 'inline-flex', alignItems: 'center', gap: '8px'}}>
            <span style={badgeStyle}>{'OC'}</span>
            {'OpenCode'}
        </span>
    );
};

const badgeStyle: React.CSSProperties = {
    alignItems: 'center',
    background: 'var(--button-bg)',
    borderRadius: '999px',
    color: 'var(--button-color)',
    display: 'inline-flex',
    fontSize: '11px',
    fontWeight: 700,
    height: '22px',
    justifyContent: 'center',
    width: '22px',
};

const HeaderIcon = () => <span style={badgeStyle}>{'OC'}</span>;

const SafeConfigSetting = (props: React.ComponentProps<typeof ConfigSetting>) => (
    <PluginErrorBoundary area={'Admin Settings'}>
        <ConfigSetting {...props}/>
    </PluginErrorBoundary>
);

const SafeRHSPane = () => (
    <PluginErrorBoundary area={'OpenCode Sidebar'}>
        <RHSPane/>
    </PluginErrorBoundary>
);

const SafeOpenCodeBotPost = (props: React.ComponentProps<typeof OpenCodeBotPost>) => (
    <PluginErrorBoundary area={'OpenCode Bot Post'}>
        <OpenCodeBotPost {...props}/>
    </PluginErrorBoundary>
);

export default class Plugin {
    private readonly postEventListener = new PostEventListener();

    public async initialize(registry: PluginRegistry, store: Store<GlobalState>) {
        let siteURL = store.getState().entities.general.config.SiteURL;
        if (!siteURL) {
            siteURL = window.location.origin;
        }
        setSiteURL(siteURL);

        if (registry.registerAdminConsoleCustomSetting) {
            registry.registerAdminConsoleCustomSetting('Config', SafeConfigSetting);
        }

        registry.registerWebSocketEventHandler(
            buildPluginWebSocketEventName(manifest.id, 'postupdate'),
            (msg) => {
                handleStreamingPostUpdateEvent(store, msg);
                this.postEventListener.handlePostUpdateWebsockets(msg as any);
            },
        );

        if (registry.registerPostTypeComponent) {
            registry.registerPostTypeComponent('custom_opencode_bot', (props: any) => (
                <SafeOpenCodeBotPost
                    {...props}
                    websocketRegister={this.postEventListener.registerPostUpdateListener}
                    websocketUnregister={this.postEventListener.unregisterPostUpdateListener}
                />
            ));
        }

        if (registry.registerRightHandSidebarComponent) {
            const rhs = registry.registerRightHandSidebarComponent(SafeRHSPane, OpenCodeTitle);
            registry.registerChannelHeaderButtonAction(
                <HeaderIcon/>,
                () => store.dispatch(rhs.toggleRHSPlugin as any),
                'OpenCode',
                'Open OpenCode',
            );
        }
    }
}

declare global {
    interface Window {
        registerPlugin(pluginId: string, plugin: Plugin): void;
    }
}

window.registerPlugin(manifest.id, new Plugin());
