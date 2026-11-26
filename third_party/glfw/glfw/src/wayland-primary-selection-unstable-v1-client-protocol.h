/* Written for 1loop in the layout of wayland-scanner 1.23.1's output, from
 * wayland-protocols' unstable/primary-selection/primary-selection-unstable-v1.xml
 * (Copyright © 2015, 2016 Red Hat, MIT licence: see the .c file). Only what
 * GLFW uses is declared: the requests and the listeners. */

#ifndef WP_PRIMARY_SELECTION_UNSTABLE_V1_CLIENT_PROTOCOL_H
#define WP_PRIMARY_SELECTION_UNSTABLE_V1_CLIENT_PROTOCOL_H

#include <stdint.h>
#include <stddef.h>
#include "wayland-client.h"

#ifdef  __cplusplus
extern "C" {
#endif

struct wl_seat;
struct zwp_primary_selection_device_manager_v1;
struct zwp_primary_selection_device_v1;
struct zwp_primary_selection_offer_v1;
struct zwp_primary_selection_source_v1;

extern const struct wl_interface zwp_primary_selection_device_manager_v1_interface;
extern const struct wl_interface zwp_primary_selection_device_v1_interface;
extern const struct wl_interface zwp_primary_selection_offer_v1_interface;
extern const struct wl_interface zwp_primary_selection_source_v1_interface;

#define ZWP_PRIMARY_SELECTION_DEVICE_MANAGER_V1_CREATE_SOURCE 0
#define ZWP_PRIMARY_SELECTION_DEVICE_MANAGER_V1_GET_DEVICE 1
#define ZWP_PRIMARY_SELECTION_DEVICE_MANAGER_V1_DESTROY 2

static inline struct zwp_primary_selection_source_v1 *
zwp_primary_selection_device_manager_v1_create_source(struct zwp_primary_selection_device_manager_v1 *manager)
{
	struct wl_proxy *id;

	id = wl_proxy_marshal_flags((struct wl_proxy *) manager,
			 ZWP_PRIMARY_SELECTION_DEVICE_MANAGER_V1_CREATE_SOURCE, &zwp_primary_selection_source_v1_interface, wl_proxy_get_version((struct wl_proxy *) manager), 0, NULL);

	return (struct zwp_primary_selection_source_v1 *) id;
}

static inline struct zwp_primary_selection_device_v1 *
zwp_primary_selection_device_manager_v1_get_device(struct zwp_primary_selection_device_manager_v1 *manager, struct wl_seat *seat)
{
	struct wl_proxy *id;

	id = wl_proxy_marshal_flags((struct wl_proxy *) manager,
			 ZWP_PRIMARY_SELECTION_DEVICE_MANAGER_V1_GET_DEVICE, &zwp_primary_selection_device_v1_interface, wl_proxy_get_version((struct wl_proxy *) manager), 0, NULL, seat);

	return (struct zwp_primary_selection_device_v1 *) id;
}

static inline void
zwp_primary_selection_device_manager_v1_destroy(struct zwp_primary_selection_device_manager_v1 *manager)
{
	wl_proxy_marshal_flags((struct wl_proxy *) manager,
			 ZWP_PRIMARY_SELECTION_DEVICE_MANAGER_V1_DESTROY, NULL, wl_proxy_get_version((struct wl_proxy *) manager), WL_MARSHAL_FLAG_DESTROY);
}

struct zwp_primary_selection_device_v1_listener {
	void (*data_offer)(void *data,
			   struct zwp_primary_selection_device_v1 *device,
			   struct zwp_primary_selection_offer_v1 *offer);
	void (*selection)(void *data,
			  struct zwp_primary_selection_device_v1 *device,
			  struct zwp_primary_selection_offer_v1 *id);
};

static inline int
zwp_primary_selection_device_v1_add_listener(struct zwp_primary_selection_device_v1 *device,
					     const struct zwp_primary_selection_device_v1_listener *listener, void *data)
{
	return wl_proxy_add_listener((struct wl_proxy *) device,
				     (void (**)(void)) listener, data);
}

#define ZWP_PRIMARY_SELECTION_DEVICE_V1_SET_SELECTION 0
#define ZWP_PRIMARY_SELECTION_DEVICE_V1_DESTROY 1

static inline void
zwp_primary_selection_device_v1_set_selection(struct zwp_primary_selection_device_v1 *device, struct zwp_primary_selection_source_v1 *source, uint32_t serial)
{
	wl_proxy_marshal_flags((struct wl_proxy *) device,
			 ZWP_PRIMARY_SELECTION_DEVICE_V1_SET_SELECTION, NULL, wl_proxy_get_version((struct wl_proxy *) device), 0, source, serial);
}

static inline void
zwp_primary_selection_device_v1_destroy(struct zwp_primary_selection_device_v1 *device)
{
	wl_proxy_marshal_flags((struct wl_proxy *) device,
			 ZWP_PRIMARY_SELECTION_DEVICE_V1_DESTROY, NULL, wl_proxy_get_version((struct wl_proxy *) device), WL_MARSHAL_FLAG_DESTROY);
}

#define ZWP_PRIMARY_SELECTION_OFFER_V1_RECEIVE 0
#define ZWP_PRIMARY_SELECTION_OFFER_V1_DESTROY 1

static inline void
zwp_primary_selection_offer_v1_destroy(struct zwp_primary_selection_offer_v1 *offer)
{
	wl_proxy_marshal_flags((struct wl_proxy *) offer,
			 ZWP_PRIMARY_SELECTION_OFFER_V1_DESTROY, NULL, wl_proxy_get_version((struct wl_proxy *) offer), WL_MARSHAL_FLAG_DESTROY);
}

struct zwp_primary_selection_source_v1_listener {
	void (*send)(void *data,
		     struct zwp_primary_selection_source_v1 *source,
		     const char *mime_type,
		     int32_t fd);
	void (*cancelled)(void *data,
			  struct zwp_primary_selection_source_v1 *source);
};

static inline int
zwp_primary_selection_source_v1_add_listener(struct zwp_primary_selection_source_v1 *source,
					     const struct zwp_primary_selection_source_v1_listener *listener, void *data)
{
	return wl_proxy_add_listener((struct wl_proxy *) source,
				     (void (**)(void)) listener, data);
}

#define ZWP_PRIMARY_SELECTION_SOURCE_V1_OFFER 0
#define ZWP_PRIMARY_SELECTION_SOURCE_V1_DESTROY 1

static inline void
zwp_primary_selection_source_v1_offer(struct zwp_primary_selection_source_v1 *source, const char *mime_type)
{
	wl_proxy_marshal_flags((struct wl_proxy *) source,
			 ZWP_PRIMARY_SELECTION_SOURCE_V1_OFFER, NULL, wl_proxy_get_version((struct wl_proxy *) source), 0, mime_type);
}

static inline void
zwp_primary_selection_source_v1_destroy(struct zwp_primary_selection_source_v1 *source)
{
	wl_proxy_marshal_flags((struct wl_proxy *) source,
			 ZWP_PRIMARY_SELECTION_SOURCE_V1_DESTROY, NULL, wl_proxy_get_version((struct wl_proxy *) source), WL_MARSHAL_FLAG_DESTROY);
}

#ifdef  __cplusplus
}
#endif

#endif
