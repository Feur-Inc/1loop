// SPDX-License-Identifier: MPL-2.0
// Slow bands of the accent over the background. Working: they move faster;
// waiting for you: they pulse; an error: a red flash that
// settles to a hint of red over the accent until the failed chat is seen.
// The bands brighten softly around the pointer while it is over the window.
void main() {
    vec2 uv = (gl_FragCoord.xy - u_origin) / u_resolution;
    float speed = u_state == STATE_WORKING ? 0.35 : 0.08;
    float t = u_time * speed;
    float band = sin(uv.x * 3.0 + t * 2.0 + sin(uv.y * 4.0 - t) * 1.5);
    band += 0.6 * sin(uv.y * 5.0 - t * 1.3 + uv.x * 2.0);
    float glow = smoothstep(0.2, 1.6, band) * (1.0 - uv.y * 0.6);
    vec3 tint = u_accent.rgb;
    if (u_state == STATE_ERROR) {
        // A red flash that settles to a hint of red over the accent.
        float flash = 0.25 + 0.75 * exp(-u_state_time * 0.5);
        tint = mix(tint, vec3(0.91, 0.42, 0.37), flash);
    }
    if (u_state == STATE_WAITING) {
        glow *= 0.6 + 0.4 * sin(u_state_time * 3.0);
    }
    // iMouse is in gl_FragCoord's pixels, like gl_FragCoord itself.
    float near = 1.0 - smoothstep(0.0, 0.35 * u_resolution.y, distance(gl_FragCoord.xy, iMouse.xy));
    glow += near * 0.35 * u_mouse_inside;
    gl_FragColor = vec4(mix(u_background.rgb, tint, glow * 0.45), 1.0);
}
