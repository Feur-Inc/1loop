// SPDX-License-Identifier: MPL-2.0
// A still gradient with a faint grain: cheap, for a shader at 15 fps.
float hash(vec2 p) { return fract(sin(dot(p, vec2(12.9898, 78.233))) * 43758.5453); }
void main() {
    vec2 uv = (gl_FragCoord.xy - u_origin) / u_resolution;
    vec3 base = mix(u_background.rgb, u_surface.rgb, uv.y);
    base = mix(base, u_accent.rgb, 0.08 * (1.0 - distance(uv, vec2(0.85, 0.9))));
    float grain = hash(floor(gl_FragCoord.xy) + floor(u_time * 8.0)) - 0.5;
    gl_FragColor = vec4(base + grain * 0.025, 1.0);
}
