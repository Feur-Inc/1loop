// SPDX-License-Identifier: MPL-2.0
// Slow bands of the accent over the background. Working: they move faster;
// waiting for you: they pulse; an error: they turn to the danger red.
void main() {
    vec2 uv = (gl_FragCoord.xy - u_origin) / u_resolution;
    float speed = u_state == STATE_WORKING ? 0.35 : 0.08;
    float t = u_time * speed;
    float band = sin(uv.x * 3.0 + t * 2.0 + sin(uv.y * 4.0 - t) * 1.5);
    band += 0.6 * sin(uv.y * 5.0 - t * 1.3 + uv.x * 2.0);
    float glow = smoothstep(0.2, 1.6, band) * (1.0 - uv.y * 0.6);
    vec3 tint = u_accent.rgb;
    if (u_state == STATE_ERROR) {
        tint = vec3(0.91, 0.42, 0.37);
    }
    if (u_state == STATE_WAITING) {
        glow *= 0.6 + 0.4 * sin(u_state_time * 3.0);
    }
    gl_FragColor = vec4(mix(u_background.rgb, tint, glow * 0.45), 1.0);
}
