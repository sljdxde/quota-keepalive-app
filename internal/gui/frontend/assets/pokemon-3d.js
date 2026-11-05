import * as THREE from "./vendor/three.module.min.js";
import { GLTFLoader } from "./vendor/GLTFLoader.js";
import { DRACOLoader } from "./vendor/DRACOLoader.js";
import {
  performanceLow,
  PERFORMANCE_LOW_FPS,
  softwareRendering,
  SOFTWARE_RENDER_FPS,
} from "./perf-mode.js";
import { gazePose, skillProfile, skillTimeline } from "./pet-motion.js";

const TARGET_HEIGHT = 2.35;
const TARGET_WIDTH = 2.9;
const TARGET_DEPTH = 3.8;
const EVOLUTION_FADE_MS = 1500;
// 头部跟随鼠标的响应曲线。取值偏小/偏大会分别表现为「慢半拍」和「抽搐」，
// 这里按 60fps 下约 90-130ms 收到位来定；角度本身由 gazePose 的角度表决定，
// 这里的 lambda 只负责「多快到位」。
const LOOK_SMOOTH_TIME_X = 0.085;
const LOOK_SMOOTH_TIME_Y = 0.1;
// 长按蓄力用于「投精灵球」：按住 CHARGE_FULL_SECONDS 秒到满，甜点区在
// pet-motion.CAPTURE_SWEET_SPOT 附近；这里只负责把蓄力状态画成下蹲与发光。
const CHARGE_FULL_SECONDS = 1.6;
const BODY_FOLLOW_LAMBDA = 7.2;
const HEAD_FOLLOW_LAMBDA = 22;
const NECK_FOLLOW_LAMBDA = 15;
// 捕获动画各段时长（毫秒）：投球 → 命中/躲避 → 吸入 → 落地 → 晃动 → 结算。
const CAPTURE_THROW_MS = 620;
const CAPTURE_DODGE_MS = 620;
const CAPTURE_SUCK_MS = 340;
const CAPTURE_DROP_MS = 320;
const CAPTURE_WOBBLE_MS = 1350;
const CAPTURE_RELEASE_MS = 520;
// 精灵球静止位置：宠物脚下正前方。
const CAPTURE_REST_X = 0;
const CAPTURE_REST_Y = -0.94;
const CAPTURE_REST_Z = 0.52;
// 粒子池上限：普通渲染约 40-110 颗，大招约 180 颗；给二次爆发与低配缩放
// 留足余量，池子满了就覆盖最老的粒子，不会因为频繁创建而触发 GC。
const VFX_MAX_PARTICLES = 420;
const VFX_MAX_RINGS = 6;
const CAMERA_BASE_Z = 5.35;
const CAMERA_BASE_Y = 0.2;
const CAMERA_BASE_FOV = 30;

function damp(current, target, lambda, delta) {
  return THREE.MathUtils.lerp(current, target, 1 - Math.exp(-lambda * delta));
}

function smoothDamp(current, velocity, target, smoothTime, delta) {
  const omega = 2 / Math.max(0.001, smoothTime);
  const x = omega * delta;
  const decay = 1 / (1 + x + 0.48 * x * x + 0.235 * x * x * x);
  const change = current - target;
  const temp = (velocity + omega * change) * delta;
  return {
    value: target + (change + temp) * decay,
    velocity: (velocity - omega * temp) * decay,
  };
}

function createContactShadow() {
  const canvas = document.createElement("canvas");
  canvas.width = 256;
  canvas.height = 128;
  const context = canvas.getContext("2d");
  const gradient = context.createRadialGradient(128, 64, 8, 128, 64, 118);
  gradient.addColorStop(0, "rgba(20, 18, 24, .32)");
  gradient.addColorStop(0.5, "rgba(20, 18, 24, .14)");
  gradient.addColorStop(1, "rgba(20, 18, 24, 0)");
  context.fillStyle = gradient;
  context.fillRect(0, 0, 256, 128);
  const texture = new THREE.CanvasTexture(canvas);
  texture.colorSpace = THREE.SRGBColorSpace;
  return texture;
}

// createPokeBall 用几何体拼一颗精灵球：上下两半球 + 黑带 + 正面按钮。
// 拆成零件是为了「打开」动画：上半球绕 X 轴翻开，露出里面的宠物。
function createPokeBall() {
  const radius = 0.26;
  const topMaterial = new THREE.MeshStandardMaterial({
    color: 0xd8342c, roughness: 0.34, metalness: 0.06,
  });
  const bottomMaterial = new THREE.MeshStandardMaterial({
    color: 0xf4f4f4, roughness: 0.42, metalness: 0.02,
  });
  const bandMaterial = new THREE.MeshStandardMaterial({
    color: 0x1b1b1f, roughness: 0.5, metalness: 0.1,
  });
  const top = new THREE.Mesh(
    new THREE.SphereGeometry(radius, 32, 16, 0, Math.PI * 2, 0, Math.PI / 2),
    topMaterial
  );
  const bottom = new THREE.Mesh(
    new THREE.SphereGeometry(radius, 32, 16, 0, Math.PI * 2, Math.PI / 2, Math.PI / 2),
    bottomMaterial
  );
  const band = new THREE.Mesh(new THREE.TorusGeometry(radius, 0.018, 8, 40), bandMaterial);
  band.rotation.x = Math.PI / 2;
  const buttonRing = new THREE.Mesh(new THREE.TorusGeometry(0.062, 0.014, 8, 24), bandMaterial);
  buttonRing.position.z = radius * 0.92;
  const button = new THREE.Mesh(
    new THREE.CircleGeometry(0.052, 20),
    new THREE.MeshStandardMaterial({ color: 0xf7f7f7, roughness: 0.3 })
  );
  button.position.z = radius * 0.99;
  const group = new THREE.Group();
  group.add(top, bottom, band, buttonRing, button);
  group.visible = false;
  // 打开时上半球绕后缘翻起，像真的球壳掀开。
  top.userData.openPivot = true;
  return { group, top, bottom, button, band, radius };
}

function materialList(root) {
  const list = [];
  root.traverse(function (object) {
    if (!object.material) return;
    const materials = Array.isArray(object.material) ? object.material : [object.material];
    materials.forEach(function (material) {
      if (!list.includes(material)) list.push(material);
    });
  });
  return list;
}

function disposeObject(root) {
  if (!root) return;
  root.traverse(function (object) {
    if (object.geometry) object.geometry.dispose();
    if (!object.material) return;
    const materials = Array.isArray(object.material) ? object.material : [object.material];
    materials.forEach(function (material) {
      if (material.map) material.map.dispose();
      if (material.normalMap) material.normalMap.dispose();
      if (material.roughnessMap) material.roughnessMap.dispose();
      if (material.metalnessMap) material.metalnessMap.dispose();
      material.dispose();
    });
  });
}

function findRigBones(root) {
  const all = [];
  root.traverse(function (object) {
    if (object.isBone || /head|neck/i.test(object.name || "")) all.push(object);
  });
  const heads = all.filter(function (object) {
    return /head/i.test(object.name || "") && !/endhead/i.test(object.name || "");
  });
  const necks = all.filter(function (object) {
    return /neck/i.test(object.name || "");
  });
  const head = heads.find(function (object) {
    return /^head(?:_\d+)?$/i.test(object.name || "");
  }) || heads[0] || null;
  let neck = necks.find(function (object) {
    return /neck3|neck_?0?3/i.test(object.name || "");
  });
  if (!neck) neck = necks[necks.length - 1] || null;
  return { head, neck };
}

function captureBone(bone) {
  if (!bone) return null;
  return {
    bone,
    base: bone.quaternion.clone(),
  };
}

function applyBoneAim(rig, aimX, aimY, delta) {
  // 角度表在 pet-motion.gazePose 里：头/颈各拿一份独立角度，指针小幅移动也
  // 能有十几度的偏转，指针到边缘时头颈合计约 50 度，读起来是真的「在看你」。
  const pose = gazePose(aimX, aimY);
  const headLocal = new THREE.Quaternion().setFromEuler(
    new THREE.Euler(pose.headPitch, pose.headYaw, pose.headRoll, "YXZ")
  );
  const neckLocal = new THREE.Quaternion().setFromEuler(
    new THREE.Euler(pose.neckPitch, pose.neckYaw, 0, "YXZ")
  );
  // 头承担大部分转向、脖子分担一部分，整体才不会像一块木板在转。
  const headAlpha = 1 - Math.exp(-HEAD_FOLLOW_LAMBDA * delta);
  const neckAlpha = 1 - Math.exp(-NECK_FOLLOW_LAMBDA * delta);
  [rig.neck && { entry: rig.neck, part: neckLocal, alpha: neckAlpha },
    rig.head && { entry: rig.head, part: headLocal, alpha: headAlpha }].forEach(function (item) {
    if (!item || !item.entry) return;
    const entry = item.entry;
    const desired = entry.base.clone().multiply(item.part);
    entry.bone.quaternion.slerp(desired, item.alpha);
  });
}

// 少量模型的绑定姿势是展开的 T-pose，直接摆在桌面上过于僵硬。
// 这里按传入的 { 骨骼名: [x, y, z] } 追加一次局部旋转，把姿势调自然。
function applyBoneAdjust(source, adjust) {
  if (!adjust) return;
  const keys = Object.keys(adjust);
  const euler = new THREE.Euler();
  source.traverse(function (object) {
    if (!object.isBone) return;
    const name = object.name || "";
    // 不同来源的模型骨骼命名不一致（LShoulder / LShoulder_59），
    // 因此按「完全相等或名称为前缀」匹配，同一套配置可以复用到多个模型。
    const key = keys.find(function (candidate) {
      return name === candidate || name.indexOf(candidate) === 0;
    });
    if (!key) return;
    const spec = adjust[key];
    if (!Array.isArray(spec)) return;
    euler.set(
      Number(spec[0]) || 0,
      Number(spec[1]) || 0,
      Number(spec[2]) || 0,
      "XYZ"
    );
    object.quaternion.multiply(
      new THREE.Quaternion().setFromEuler(euler)
    );
  });
  source.updateMatrixWorld(true);
}

function prepareModel(source) {
  source.updateMatrixWorld(true);
  const initial = new THREE.Box3().setFromObject(source, true);
  const initialSize = initial.getSize(new THREE.Vector3());
  const scale = Math.min(
    TARGET_HEIGHT / Math.max(0.001, initialSize.y),
    TARGET_WIDTH / Math.max(0.001, initialSize.x),
    TARGET_DEPTH / Math.max(0.001, initialSize.z)
  );
  source.scale.setScalar(scale);
  source.updateMatrixWorld(true);

  const scaled = new THREE.Box3().setFromObject(source, true);
  const center = scaled.getCenter(new THREE.Vector3());
  source.position.x -= center.x;
  source.position.y -= scaled.min.y;
  source.updateMatrixWorld(true);

  source.traverse(function (object) {
    if (!object.isMesh) return;
    object.frustumCulled = false;
    object.castShadow = false;
    object.receiveShadow = false;
  });
  return source;
}

// 技能特效层：粒子、冲击环、屏幕闪光、点光与镜头冲击共用一套池化实例。
// 单独抽出来是为了两件事：
// 1. 同一帧固定只更新一次（粒子寿命、环缩放、闪光衰减都靠 delta 推进），
//    不再由 applySkill 里的 sin 波形假装特效；
// 2. 低配/软件渲染下只改 density 与 dpr，特效代码本身不需要分叉。
const PARTICLE_VERTEX_SHADER = `
attribute float aSize;
attribute float aAlpha;
attribute vec3 aColor;
uniform float uScale;
varying float vAlpha;
varying vec3 vColor;
void main() {
  vAlpha = aAlpha;
  vColor = aColor;
  vec4 mv = modelViewMatrix * vec4(position, 1.0);
  gl_PointSize = aSize * uScale / max(0.01, -mv.z);
  gl_Position = projectionMatrix * mv;
}`;

const PARTICLE_FRAGMENT_SHADER = `
varying float vAlpha;
varying vec3 vColor;
void main() {
  vec2 uv = gl_PointCoord - 0.5;
  float d = length(uv);
  float alpha = smoothstep(0.5, 0.06, d) * vAlpha;
  if (alpha <= 0.002) discard;
  gl_FragColor = vec4(vColor, alpha);
}`;

// 屏幕闪光用的柔边圆斑：粒子与闪光都靠 alpha 渐变，不用贴图。
function createSkillEffects(scene, camera, options) {
  const settings = options || {};
  let dpr = 1.6;
  const positions = new Float32Array(VFX_MAX_PARTICLES * 3);
  const colors = new Float32Array(VFX_MAX_PARTICLES * 3);
  const sizes = new Float32Array(VFX_MAX_PARTICLES);
  const alphas = new Float32Array(VFX_MAX_PARTICLES);
  const velocity = new Float32Array(VFX_MAX_PARTICLES * 3);
  const age = new Float32Array(VFX_MAX_PARTICLES);
  const life = new Float32Array(VFX_MAX_PARTICLES);
  const sizeBase = new Float32Array(VFX_MAX_PARTICLES);
  const yAccel = new Float32Array(VFX_MAX_PARTICLES);
  const colorFrom = new Float32Array(VFX_MAX_PARTICLES * 3);
  const colorTo = new Float32Array(VFX_MAX_PARTICLES * 3);
  let cursor = 0;

  const geometry = new THREE.BufferGeometry();
  const positionAttribute = new THREE.BufferAttribute(positions, 3)
    .setUsage(THREE.DynamicDrawUsage);
  const colorAttribute = new THREE.BufferAttribute(colors, 3)
    .setUsage(THREE.DynamicDrawUsage);
  const sizeAttribute = new THREE.BufferAttribute(sizes, 1)
    .setUsage(THREE.DynamicDrawUsage);
  const alphaAttribute = new THREE.BufferAttribute(alphas, 1)
    .setUsage(THREE.DynamicDrawUsage);
  geometry.setAttribute("position", positionAttribute);
  geometry.setAttribute("aColor", colorAttribute);
  geometry.setAttribute("aSize", sizeAttribute);
  geometry.setAttribute("aAlpha", alphaAttribute);
  const material = new THREE.ShaderMaterial({
    uniforms: { uScale: { value: 155 } },
    vertexShader: PARTICLE_VERTEX_SHADER,
    fragmentShader: PARTICLE_FRAGMENT_SHADER,
    transparent: true,
    depthWrite: false,
    depthTest: true,
    blending: THREE.AdditiveBlending,
  });
  const points = new THREE.Points(geometry, material);
  points.frustumCulled = false;
  points.renderOrder = 6;
  scene.add(points);

  // 冲击环：一圈 ring 走完「从小放大 + 淡出」，档位越高圈越多。
  const ringGeometry = new THREE.RingGeometry(0.42, 0.5, 64);
  const rings = [];
  for (let index = 0; index < VFX_MAX_RINGS; index += 1) {
    const ringMaterial = new THREE.MeshBasicMaterial({
      color: 0xffffff,
      transparent: true,
      opacity: 0,
      depthWrite: false,
      depthTest: false,
      side: THREE.DoubleSide,
      blending: THREE.AdditiveBlending,
    });
    const mesh = new THREE.Mesh(ringGeometry, ringMaterial);
    mesh.visible = false;
    mesh.renderOrder = 7;
    scene.add(mesh);
    rings.push({ mesh, material: ringMaterial, active: false, age: 0, delay: 0, life: 0.66, from: 0.5, to: 2.6 });
  }

  // 屏幕闪光：宠物前方一片柔光亮面，强度随档位与 release 包络衰减。
  const flashMaterial = new THREE.MeshBasicMaterial({
    color: 0xffffff,
    transparent: true,
    opacity: 0,
    depthWrite: false,
    depthTest: false,
    blending: THREE.AdditiveBlending,
  });
  const flash = new THREE.Mesh(new THREE.PlaneGeometry(7.6, 4.6), flashMaterial);
  flash.position.set(0, 0.55, 1.55);
  flash.renderOrder = 8;
  scene.add(flash);

  const light = new THREE.PointLight(0xffffff, 0, 0, 7, 2);
  light.position.set(0, 0.8, 1.7);
  scene.add(light);

  let flashAge = 0;
  let flashMax = 0;
  let lightAge = 0;
  let lightMax = 0;
  let punchAge = 0;
  let punchMax = 0;
  let shakeSeed = 0;
  let chargeGlow = 0;
  let secondaryAge = -1;
  let secondaryProfile = null;
  let muzzle = new THREE.Vector3(0, 0.55, 0.5);

  function writeColor(target, colorHex, offset) {
    const color = new THREE.Color(colorHex);
    target[offset] = color.r;
    target[offset + 1] = color.g;
    target[offset + 2] = color.b;
  }

  function emitParticles(profile, origin, aimX, aimY, countScale) {
    // countScale 用于二次爆发/星点：缺省必须是 1，否则 NaN 会让粒子整批消失。
    const scale = Number.isFinite(Number(countScale)) ? Number(countScale) : 1;
    const count = Math.max(0, Math.round(profile.particles * scale));
    if (!count) return;
    const spread = profile.spread;
    const aimRight = Number.isFinite(aimX) ? aimX : 0;
    const aimUp = Number.isFinite(aimY) ? -aimY : 0;
    for (let index = 0; index < count; index += 1) {
      const slot = cursor % VFX_MAX_PARTICLES;
      cursor += 1;
      const base = slot * 3;
      // 起点在头部附近的小球内，避免所有粒子都从一个点冒出来。
      positions[base] = origin.x + (Math.random() - 0.5) * 0.34;
      positions[base + 1] = origin.y + (Math.random() - 0.5) * 0.26;
      positions[base + 2] = origin.z + (Math.random() - 0.5) * 0.24;
      const speed = profile.speed * (0.55 + Math.random() * 0.9);
      const theta = Math.random() * Math.PI * 2;
      const radius = Math.random() * spread;
      const vx = Math.cos(theta) * radius * speed + aimRight * speed * 0.4;
      const vy = Math.sin(theta) * radius * speed * 0.85 + aimUp * speed * 0.34 +
        (profile.yAccel > 0 ? speed * 0.5 : 0);
      // 朝摄像机方向喷出：观众看到的是「冲着自己来」的爆发。
      const vz = profile.forward * speed * (0.35 + Math.random() * 0.5);
      velocity[base] = vx;
      velocity[base + 1] = vy;
      velocity[base + 2] = vz;
      yAccel[slot] = profile.yAccel;
      age[slot] = 0;
      life[slot] = profile.life[0] +
        Math.random() * (profile.life[1] - profile.life[0]);
      sizeBase[slot] = profile.size[0] +
        Math.random() * (profile.size[1] - profile.size[0]);
      sizes[slot] = sizeBase[slot];
      alphas[slot] = 1;
      const from = profile.colors[0];
      const to = profile.colors[1 + (index % (profile.colors.length - 1))];
      writeColor(colorFrom, from, base);
      writeColor(colorTo, to, base);
      colors[base] = colorFrom[base];
      colors[base + 1] = colorFrom[base + 1];
      colors[base + 2] = colorFrom[base + 2];
    }
    positionAttribute.needsUpdate = true;
    sizeAttribute.needsUpdate = true;
    alphaAttribute.needsUpdate = true;
    colorAttribute.needsUpdate = true;
  }

  function activateRings(profile, origin) {
    const count = Math.min(VFX_MAX_RINGS, profile.rings);
    const scale = profile.ringScale;
    for (let index = 0; index < count; index += 1) {
      const ring = rings[index];
      ring.active = true;
      ring.age = 0;
      ring.delay = index * 0.075;
      ring.life = 0.56 + index * 0.06;
      ring.from = 0.42 * scale;
      ring.to = (2.1 + profile.power * 0.45) * scale;
      ring.mesh.position.set(origin.x, origin.y + 0.02, origin.z + 0.12);
      ring.material.color.setHex(profile.ringColor);
      ring.mesh.visible = false;
    }
  }

  function burst(profile, origin, aimX, aimY, countScale) {
    muzzle = origin.clone();
    emitParticles(profile, origin, aimX, aimY, countScale);
    activateRings(profile, origin);
    flashAge = 0;
    flashMax = profile.flash;
    flashMaterial.color.setHex(profile.flashColor);
    lightAge = 0;
    lightMax = Math.max(lightMax * 0.3, 0.9 + profile.power * 0.55);
    light.color.setHex(profile.lightColor);
    punchAge = 0;
    punchMax = profile.punch;
    shakeSeed = Math.random() * 1000;
    if (profile.secondary) {
      secondaryAge = 0;
      secondaryProfile = profile;
    } else {
      secondaryAge = -1;
      secondaryProfile = null;
    }
  }

  function update(delta, aimX, aimY) {
    const dt = Math.max(0.001, Math.min(0.05, delta));
    for (let slot = 0; slot < VFX_MAX_PARTICLES; slot += 1) {
      if (alphas[slot] <= 0.002) continue;
      age[slot] += dt;
      const progress = age[slot] / Math.max(0.05, life[slot]);
      if (progress >= 1) {
        alphas[slot] = 0;
        continue;
      }
      const base = slot * 3;
      velocity[base + 1] += yAccel[slot] * dt;
      positions[base] += velocity[base] * dt;
      positions[base + 1] += velocity[base + 1] * dt;
      positions[base + 2] += velocity[base + 2] * dt;
      // 先快速亮起再淡出：0-12% 保持满亮度，之后按平方衰减。
      const fadeIn = Math.min(1, progress / 0.12);
      const fadeOut = 1 - Math.pow(progress, 1.8);
      alphas[slot] = Math.max(0, fadeIn * fadeOut);
      sizes[slot] = sizeBase[slot] * (1 + progress * 0.45);
      const mix = Math.min(1, progress * 1.35);
      colors[base] = colorFrom[base] + (colorTo[base] - colorFrom[base]) * mix;
      colors[base + 1] = colorFrom[base + 1] + (colorTo[base + 1] - colorFrom[base + 1]) * mix;
      colors[base + 2] = colorFrom[base + 2] + (colorTo[base + 2] - colorFrom[base + 2]) * mix;
    }
    positionAttribute.needsUpdate = true;
    sizeAttribute.needsUpdate = true;
    alphaAttribute.needsUpdate = true;
    colorAttribute.needsUpdate = true;

    rings.forEach(function (ring) {
      if (!ring.active) return;
      ring.age += dt;
      const local = ring.age - ring.delay;
      if (local < 0) { ring.mesh.visible = false; return; }
      const progress = local / ring.life;
      if (progress >= 1) {
        ring.active = false;
        ring.mesh.visible = false;
        ring.material.opacity = 0;
        return;
      }
      ring.mesh.visible = true;
      const eased = 1 - Math.pow(1 - progress, 2.6);
      ring.mesh.scale.setScalar(ring.from + (ring.to - ring.from) * eased);
      ring.material.opacity = Math.pow(1 - progress, 1.5) * 0.85;
    });

    flashAge += dt;
    const flashOpacity = flashMax * Math.exp(-flashAge * 7.2);
    flashMaterial.opacity = flashOpacity > 0.002 ? flashOpacity : 0;
    flash.visible = flashOpacity > 0.002;

    lightAge += dt;
    const lightLevel = lightMax * Math.exp(-lightAge * 5.2) + chargeGlow * 0.5;
    light.intensity = lightLevel;
    light.visible = lightLevel > 0.02;

    punchAge += dt;
    const punch = punchMax * Math.exp(-punchAge * 6.4);
    let shakeX = 0;
    let shakeY = 0;
    if (punch > 0.05) {
      shakeX = Math.sin((shakeSeed + punchAge * 90)) * punch * 0.14;
      shakeY = Math.cos((shakeSeed + punchAge * 77)) * punch * 0.1;
    }
    camera.position.z = CAMERA_BASE_Z - punch * 0.9;
    camera.position.x = shakeX;
    camera.position.y = CAMERA_BASE_Y + punch * 0.5 + shakeY;
    const fov = CAMERA_BASE_FOV - punch * 15;
    if (Math.abs(camera.fov - fov) > 0.01) {
      camera.fov = fov;
      camera.updateProjectionMatrix();
    }

    if (secondaryAge >= 0 && secondaryProfile) {
      secondaryAge += dt;
      if (secondaryAge >= 0.24) {
        emitParticles(secondaryProfile, muzzle, aimX, aimY, 0.55);
        const second = Math.min(VFX_MAX_RINGS - 1, secondaryProfile.rings);
        if (rings[second]) {
          const ring = rings[second];
          ring.active = true;
          ring.age = 0;
          ring.delay = 0;
          ring.life = 0.62;
          ring.from = 0.8 * secondaryProfile.ringScale;
          ring.to = (2.6 + secondaryProfile.power * 0.4) * secondaryProfile.ringScale;
          ring.mesh.position.set(muzzle.x, muzzle.y + 0.02, muzzle.z + 0.12);
          ring.material.color.setHex(secondaryProfile.ringColor);
        }
        secondaryAge = -1;
        secondaryProfile = null;
      }
    }
  }

  function reset() {
    flashMax = 0;
    lightMax = 0;
    punchMax = 0;
    flashMaterial.opacity = 0;
    flash.visible = false;
    light.intensity = 0;
    light.visible = false;
    secondaryAge = -1;
    secondaryProfile = null;
    for (let slot = 0; slot < VFX_MAX_PARTICLES; slot += 1) alphas[slot] = 0;
    alphaAttribute.needsUpdate = true;
    rings.forEach(function (ring) {
      ring.active = false;
      ring.mesh.visible = false;
      ring.material.opacity = 0;
    });
    camera.position.set(0, CAMERA_BASE_Y, CAMERA_BASE_Z);
    if (camera.fov !== CAMERA_BASE_FOV) {
      camera.fov = CAMERA_BASE_FOV;
      camera.updateProjectionMatrix();
    }
  }

  return {
    setViewport: function (cssWidth, cssHeight, nextDpr) {
      dpr = Math.max(1, Number(nextDpr) || 1);
      const height = Math.max(1, Number(cssHeight) || 1);
      material.uniforms.uScale.value = height * 0.5 * dpr;
    },
    setChargeGlow: function (value) {
      chargeGlow = Math.max(0, Math.min(1, Number(value) || 0));
    },
    burst,
    // stats 只服务调试与单测：看看池子里活着几颗、闪光/冲击处在什么强度。
    stats: function () {
      let alive = 0;
      for (let slot = 0; slot < VFX_MAX_PARTICLES; slot += 1) {
        if (alphas[slot] > 0.002) alive += 1;
      }
      return {
        alive,
        flash: Number(flashMaterial.opacity.toFixed(3)),
        light: Number(light.intensity.toFixed(3)),
        punch: Number((punchMax * Math.exp(-punchAge * 6.4)).toFixed(3)),
        ringActive: rings.filter(function (ring) { return ring.active; }).length,
      };
    },
    // sparkle 用同一套池子做「捕获成功/放出」的星点爆发：不需要技能参数。
    sparkle: function (origin, colorHex, power) {
      const level = Math.max(1, Math.min(5, Number(power) || 2));
      burst({
        particles: 20 + level * 6,
        rings: 1,
        power: level,
        flash: 0.1,
        punch: 0.02,
        colors: [colorHex || 0xffe066, 0xffffff],
        ringColor: colorHex || 0xffe066,
        flashColor: colorHex || 0xffe066,
        lightColor: colorHex || 0xffe066,
        speed: 2.2,
        spread: 1,
        forward: 0.7,
        yAccel: -1.4,
        size: [0.07, 0.16],
        life: [0.3, 0.62],
        ringScale: 0.8,
        secondary: false,
      }, origin, 0, 0);
    },
    update,
    reset,
    dispose: function () {
      scene.remove(points);
      geometry.dispose();
      material.dispose();
      rings.forEach(function (ring) {
        scene.remove(ring.mesh);
        ring.material.dispose();
      });
      ringGeometry.dispose();
      scene.remove(flash);
      flash.geometry.dispose();
      flashMaterial.dispose();
      scene.remove(light);
    },
  };
}

function createStageViewer(canvas, options) {
  const settings = options || {};
  const models = Array.isArray(settings.models) ? settings.models : [];
  const reduceMotion = !!(window.matchMedia &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches);
  let stage = Math.max(1, Math.min(models.length, Number(settings.stage) || 1));
  let enabled = settings.enabled !== false;
  let closed = false;
  let loadingToken = 0;
  let animationFrame = 0;
  let lastTime = performance.now();
  let elapsed = 0;
  let aimX = 0;
  let aimY = 0;
  let currentAimX = 0;
  let currentAimY = 0;
  let aimVelocityX = 0;
  let aimVelocityY = 0;
  let bodyAimX = 0;
  let current = null;
  let currentRig = null;
  let transition = null;
  let effectIntensity = Math.max(1, Math.min(5, Number(settings.effectIntensity) || 1));
  let skillStart = -1;
  let skillType = "";
  let skillPower = 1;
  let skillUltimate = false;
  // skillVfx 是本次释放缓存下来的参数表（pet-motion.skillProfile 的结果）；
  // skillEmitted 保证一次释放只在 releaseAt 那一刻爆一次，而不是每帧叠加。
  let skillVfx = null;
  let skillEmitted = true;
  // 长按蓄力：chargeStart>=0 表示正在蓄力，0..1 的进度驱动下蹲、抖动与自发光。
  let chargeStart = -1;
  let chargeGlow = 0;
  let resizeObserver = null;
  let lastDrawTime = 0;

  // 低配模式下降采样：DPR 2 的屏幕上像素量降到 1/4，软件渲染下收益最大。
  function pixelRatioCap() {
    return performanceLow() ? 1 : 1.6;
  }

  const renderer = new THREE.WebGLRenderer({
    canvas,
    alpha: true,
    antialias: !performanceLow(),
    powerPreference: "low-power",
  });
  renderer.setClearColor(0x000000, 0);
  renderer.outputColorSpace = THREE.SRGBColorSpace;
  renderer.toneMapping = THREE.ACESFilmicToneMapping;
  renderer.toneMappingExposure = 1.12;
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, pixelRatioCap()));

  const scene = new THREE.Scene();
  const camera = new THREE.PerspectiveCamera(30, 1, 0.1, 40);
  camera.position.set(0, 0.2, 5.35);
  camera.lookAt(0, 0.05, 0);

  // 技能特效层在场景创建后立刻建好：池子常驻，释放技能只是激活槽位。
  const effects = createSkillEffects(scene, camera, {
    reduceMotion: reduceMotion,
  });
  effects.setViewport(canvas.clientWidth || 310, canvas.clientHeight || 310,
    renderer.getPixelRatio());

  scene.add(new THREE.HemisphereLight(0xfffaf0, 0x263140, 2.7));
  const key = new THREE.DirectionalLight(0xffe4bb, 3.8);
  key.position.set(-3.5, 5.2, 4.6);
  scene.add(key);
  const fill = new THREE.DirectionalLight(0xbed8ff, 2.4);
  fill.position.set(4, 2.2, 3.4);
  scene.add(fill);
  const rim = new THREE.DirectionalLight(0xffc06a, 2.25);
  rim.position.set(-2.6, 2.1, -3.5);
  scene.add(rim);

  const shadow = new THREE.Mesh(
    new THREE.PlaneGeometry(3.1, 1.15),
    new THREE.MeshBasicMaterial({
      map: createContactShadow(),
      transparent: true,
      depthWrite: false,
      opacity: 0.72,
    })
  );
  shadow.rotation.x = -Math.PI / 2;
  shadow.position.set(0, -1.16, 0.08);
  scene.add(shadow);

  // 精灵球：捕获成功后停在宠物脚下，点它把伙伴放出来。
  const ball = createPokeBall();
  ball.group.position.set(CAPTURE_REST_X, CAPTURE_REST_Y, CAPTURE_REST_Z);
  scene.add(ball.group);
  let caught = false;
  let ballOpen = 0;
  // capture 是当前正在播放的捕获动画：
  //   { phase: "throw"|"dodge"|"suck"|"drop"|"wobble"|"release", start, plan, ... }
  let capture = null;

  const draco = new DRACOLoader();
  draco.setDecoderPath(new URL("./assets/vendor/draco/", document.baseURI).href);
  const loader = new GLTFLoader();
  loader.setDRACOLoader(draco);

  function notifyFallback() {
    if (settings.onUnavailable) settings.onUnavailable();
  }

  function renderOnce() {
    if (!closed) renderer.render(scene, camera);
  }

  function resize() {
    const rect = canvas.getBoundingClientRect();
    const width = Math.max(1, Math.round(rect.width));
    const height = Math.max(1, Math.round(rect.height));
    renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, pixelRatioCap()));
    renderer.setSize(width, height, false);
    camera.aspect = width / height;
    camera.updateProjectionMatrix();
    // 粒子的点尺寸按 CSS 高度换算：不随窗口缩放变化大小。
    effects.setViewport(width, height, renderer.getPixelRatio());
    renderOnce();
  }

  function addLoadedModel(root, sceneStage) {
    const rotation = settings.modelRotations && settings.modelRotations[sceneStage - 1];
    if (Array.isArray(rotation)) {
      root.rotation.set(
        Number(rotation[0]) || 0,
        Number(rotation[1]) || 0,
        Number(rotation[2]) || 0
      );
    }
    applyBoneAdjust(root, settings.boneAdjust);
    prepareModel(root);
    const rig = findRigBones(root);
    const holder = new THREE.Group();
    holder.add(root);
    holder.position.y = -1.1;
    holder.userData.stage = sceneStage;
    root.rotation.y = settings.modelYaw || 0;
    scene.add(holder);
    const materials = materialList(root);
    materials.forEach(function (material) {
      // 低模导出常带着 material metallic=0.4 的默认值，又没有任何金属度贴图。
      // 场景里没有环境贴图时，这部分金属度只会把固有色压暗发灰，所以统一
      // 压成哑光、抬高粗糙度，让宝可梦回到平涂卡通的观感。
      if (material.metalness !== undefined && !material.metalnessMap) {
        material.metalness = 0;
      }
      if (material.roughness !== undefined &&
          !material.roughnessMap && material.roughness < 0.78) {
        material.roughness = 0.78;
      }
      if (material.envMapIntensity !== undefined) {
        material.envMapIntensity = 0.65;
      }
      material.userData.qkaOpacity = Number.isFinite(material.opacity) ? material.opacity : 1;
      material.userData.qkaTransparent = !!material.transparent;
      material.userData.qkaDepthWrite = material.depthWrite !== false;
      material.transparent = material.userData.qkaTransparent;
      material.needsUpdate = true;
    });
    return {
      root,
      holder,
      rig: {
        head: captureBone(rig.head),
        neck: captureBone(rig.neck),
      },
      materials,
      baseY: holder.position.y,
    };
  }

  function setOpacity(entry, opacity) {
    entry.materials.forEach(function (material) {
      const value = Math.max(0, Math.min(1, Number(opacity) || 0));
      material.opacity = material.userData.qkaOpacity * value;
      const transparent = material.userData.qkaTransparent || value < 0.999;
      const depthWrite = value >= 0.999 ? material.userData.qkaDepthWrite : false;
      if (material.transparent !== transparent || material.depthWrite !== depthWrite) {
        material.transparent = transparent;
        material.depthWrite = depthWrite;
        material.needsUpdate = true;
      }
    });
  }

  async function loadStage(nextStage) {
    const url = models[nextStage - 1];
    if (!url) throw new Error("missing pokemon model");
    const gltf = await loader.loadAsync(url);
    return addLoadedModel(gltf.scene, nextStage);
  }

  async function setStage(nextStage, animate) {
    const targetStage = Math.max(1, Math.min(models.length, Number(nextStage) || 1));
    if (targetStage === stage && current) return;
    stage = targetStage;
    const token = ++loadingToken;
    if (settings.onLoading) settings.onLoading(true);
    try {
      const next = await loadStage(targetStage);
      if (closed || token !== loadingToken) {
        disposeObject(next.root);
        return;
      }
      setOpacity(next, 0);
      next.holder.scale.setScalar(0.82);
      if (!current) {
        current = next;
        currentRig = next.rig;
        setOpacity(next, 1);
        next.holder.scale.setScalar(1);
        if (settings.onReady) settings.onReady();
      } else {
        transition = {
          from: current,
          to: next,
          start: performance.now(),
          duration: animate === false || reduceMotion ? 0 : EVOLUTION_FADE_MS,
        };
        current = next;
        currentRig = next.rig;
      }
      renderOnce();
    } catch (error) {
      if (token === loadingToken && settings.onError) settings.onError(error);
      notifyFallback();
    } finally {
      if (token === loadingToken && settings.onLoading) settings.onLoading(false);
    }
  }

  // skillOrigin 取头部骨骼的世界坐标：技能从脸前喷出，而不是从脚底冒烟。
// 模型没有头骨时退回胸口高度。
function skillOrigin() {
  const origin = new THREE.Vector3(0, 0.62, 0.42);
  if (currentRig && currentRig.head && currentRig.head.bone) {
    currentRig.head.bone.getWorldPosition(origin);
    origin.y += 0.04;
    origin.z += 0.24;
  }
  return origin;
}

  function applySkill(time) {
    if (!current || skillStart < 0) return;
    if (!skillVfx) {
      skillVfx = skillProfile(skillType, skillPower, skillUltimate, {});
    }
    const duration = skillVfx.duration;
    const progress = (time - skillStart) / duration;
    if (progress >= 1) {
      skillStart = -1;
      skillUltimate = false;
      skillVfx = null;
      skillEmitted = true;
      return;
    }
    const envelope = Math.sin(progress * Math.PI);
    const amplify = skillUltimate ? 1.45 : 1;
    const timeline = skillTimeline(progress);
    const wave = Math.sin(progress * Math.PI * (skillType === "electric" ? 13 : 5));
    const holder = current.holder;
    holder.position.y = current.baseY +
      Math.abs(wave) * 0.055 * skillPower * envelope * amplify -
      timeline.windup * 0.05 * skillPower;
    holder.rotation.z = wave * 0.035 * skillPower * envelope * amplify;
    holder.rotation.y = -wave * 0.025 * skillPower * envelope * amplify;
    const pulse = 1 + envelope * (0.025 + skillPower * 0.012) * amplify;
    holder.scale.setScalar(pulse);
    if (skillUltimate) {
      // 二次冲击：后半段再叠一个快速衰减的纵向弹跳。
      const settle = Math.max(0, (progress - 0.55) / 0.45);
      const recoil = Math.sin(settle * Math.PI) * (1 - settle);
      holder.position.y += recoil * 0.09 * skillPower;
      holder.scale.setScalar(pulse - recoil * 0.05);
    }
    if (skillType === "electric") {
      holder.position.x = wave * 0.055 * skillPower * envelope * amplify;
    } else if (skillType === "water") {
      holder.scale.set(
        1 + envelope * 0.04 * skillPower * amplify,
        1 - envelope * 0.025 * amplify,
        1
      );
    } else if (skillType === "leaf") {
      holder.rotation.z += Math.sin(progress * Math.PI * 8) * 0.05 * envelope * amplify;
    } else if (skillType === "ember") {
      // 火焰向后坐一下再冲出去，和喷射方向相反、力量感更足。
      holder.position.y -= timeline.windup * 0.03;
      holder.rotation.x = -timeline.windup * 0.06;
    }
    // 释放瞬间只触发一次：粒子、冲击环、闪光、点光、镜头冲击一起上。
    if (!skillEmitted && progress >= skillVfx.releaseAt) {
      skillEmitted = true;
      effects.burst(skillVfx, skillOrigin(), currentAimX, currentAimY);
    }
  }

  // applyCharge 在蓄力期间接管姿态：下蹲、轻微抖动、自发光渐强。
  // 长按越久 glow 越足，玩家能看出「再按住就要放大招了」。
  function applyCharge(time) {
    if (!current || chargeStart < 0) return;
    const held = Math.max(0, (time - chargeStart) / 1000);
    chargeGlow = Math.min(1, held / CHARGE_FULL_SECONDS);
    const holder = current.holder;
    const tremble = Math.sin(held * 26) * 0.012 * chargeGlow;
    const crouch = 0.075 * chargeGlow;
    holder.position.y = current.baseY - crouch;
    holder.rotation.z = tremble;
    holder.rotation.x = 0;
    const squash = 1 + chargeGlow * 0.04;
    holder.scale.set(squash, 1 - chargeGlow * 0.06, squash);
    // 自发光只做「边缘发亮」的提示，不能把固有色冲掉：颜色降低饱和度、
    // 强度控制在基础值的 +0.65 以内，蓄满时仍看得清五官和花纹。
    const glow = chargeGlow * 0.22;
    current.materials.forEach(function (material) {
      if (!material.emissive) return;
      if (material.userData.qkaBaseEmissive === undefined) {
        material.userData.qkaBaseEmissive = material.emissive.getHex();
        material.userData.qkaBaseEmissiveIntensity =
          Number.isFinite(material.emissiveIntensity) ? material.emissiveIntensity : 1;
      }
      const accent = new THREE.Color(settings.chargeColor || 0xffc46b);
      accent.lerp(new THREE.Color(0x2a1a08), 0.55);
      material.emissive.copy(accent);
      material.emissiveIntensity =
        material.userData.qkaBaseEmissiveIntensity + glow * 0.45;
      material.needsUpdate = true;
    });
  }

  function clearChargeGlow() {
    if (!current) return;
    current.materials.forEach(function (material) {
      if (!material.emissive || material.userData.qkaBaseEmissive === undefined) return;
      material.emissive.setHex(material.userData.qkaBaseEmissive);
      material.emissiveIntensity = material.userData.qkaBaseEmissiveIntensity;
      material.needsUpdate = true;
    });
    chargeGlow = 0;
  }

  // ---- 捕获（投精灵球） ----
  // 概率与躲避结果由 UI 层用 pet-motion.captureOutcome 算好，这里只负责
  // 把 plan.willDodge / plan.willCatch 演出来，并在动画结束时回调 onResolved。

  function captureTarget() {
    const target = new THREE.Vector3(0, 0.05, 0.5);
    if (current) {
      // holder 把模型底边放在 y=-1.1，头胸大约在 +0.9~1.2 的高度。
      target.set(
        current.holder.position.x,
        current.holder.position.y + 1.0,
        current.holder.position.z + 0.3
      );
    }
    return target;
  }

  function throwBall(plan) {
    if (closed || !current || caught || capture) return false;
    if (chargeStart >= 0) {
      chargeStart = -1;
      clearChargeGlow();
    }
    // 投球打断正在播放的技能：两套动画同时改姿态会让宠物抖成一片。
    skillStart = -1;
    skillVfx = null;
    skillEmitted = true;
    capture = {
      phase: "throw",
      start: performance.now(),
      plan: plan || {},
      startPos: new THREE.Vector3(-1.5, -1.15, 1.05),
      target: captureTarget(),
      dodgeDir: 1,
      impact: new THREE.Vector3(),
    };
    ball.group.visible = true;
    ball.group.position.copy(capture.startPos);
    ball.group.rotation.set(0, 0, 0);
    ball.group.scale.setScalar(0.55);
    ball.top.rotation.x = 0;
    ballOpen = 0;
    return true;
  }

  function notifyCaptureResolved(result) {
    const plan = capture && capture.plan;
    if (plan && typeof plan.onResolved === "function") plan.onResolved(result);
  }

  function applyCapture(time) {
    if (caught) {
      if (current) current.holder.visible = false;
      ball.group.visible = true;
      if (!capture) {
        ball.group.position.set(CAPTURE_REST_X, CAPTURE_REST_Y, CAPTURE_REST_Z);
        ball.group.rotation.set(0, 0, 0);
        ball.group.scale.setScalar(1);
        ball.top.rotation.x = 0;
      }
    }
    if (!capture) return;
    if (capture.phase === "throw") {
      const t = Math.min(1, (time - capture.start) / CAPTURE_THROW_MS);
      const eased = 1 - Math.pow(1 - t, 2.4);
      const from = capture.startPos;
      const to = capture.target;
      ball.group.position.set(
        from.x + (to.x - from.x) * eased,
        from.y + (to.y - from.y) * eased + Math.sin(t * Math.PI) * 1.45,
        from.z + (to.z - from.z) * eased
      );
      ball.group.scale.setScalar(0.55 + eased * 0.45);
      ball.group.rotation.z = t * 11;
      ball.group.rotation.x = t * 7;
      if (t >= 1) {
        capture.impact.copy(ball.group.position);
        effects.sparkle(capture.impact.clone(), 0xffffff, 2);
        capture.phase = capture.plan.willDodge ? "dodge" : "suck";
        capture.dodgeDir = Math.random() < 0.5 ? -1 : 1;
        capture.start = time;
      }
      return;
    }
    if (capture.phase === "dodge") {
      const t = Math.min(1, (time - capture.start) / CAPTURE_DODGE_MS);
      // 躲避幅度控制在半帧内：再大就会跳到画布外，看起来像「消失」而不是躲开。
      const hop = Math.sin(Math.min(1, t * 1.35) * Math.PI) * 0.55;
      if (current) {
        current.holder.position.x += capture.dodgeDir * hop;
        current.holder.position.y += Math.abs(Math.sin(t * Math.PI * 2)) * 0.1;
        current.holder.rotation.z -= capture.dodgeDir * hop * 0.26;
      }
      ball.group.position.set(
        capture.impact.x + capture.dodgeDir * 1.7 * t,
        capture.impact.y + Math.sin(t * Math.PI) * 0.5,
        capture.impact.z + 0.9 * t
      );
      ball.group.scale.setScalar(Math.max(0.05, 1 - t * 0.95));
      if (t >= 1) {
        capture = null;
        ball.group.visible = false;
        notifyCaptureResolved({ caught: false, dodged: true });
      }
      return;
    }
    if (capture.phase === "suck") {
      const t = Math.min(1, (time - capture.start) / CAPTURE_SUCK_MS);
      const eased = t * t * (3 - 2 * t);
      if (current) {
        current.holder.scale.setScalar(Math.max(0.02, 1 - eased));
        current.holder.rotation.y += t * 5;
        current.holder.visible = t < 0.99;
      }
      ball.group.scale.setScalar(1 + t * 0.12);
      if (t >= 1) {
        capture.phase = "drop";
        capture.start = time;
        capture.ballFrom = ball.group.position.clone();
      }
      return;
    }
    if (capture.phase === "drop") {
      const t = Math.min(1, (time - capture.start) / CAPTURE_DROP_MS);
      const eased = t * t;
      const from = capture.ballFrom;
      ball.group.position.set(
        from.x + (CAPTURE_REST_X - from.x) * eased,
        from.y + (CAPTURE_REST_Y - from.y) * eased,
        from.z + (CAPTURE_REST_Z - from.z) * eased
      );
      ball.group.rotation.z = Math.sin(t * Math.PI) * 0.12;
      if (t >= 1) {
        capture.phase = "wobble";
        capture.start = time;
        effects.sparkle(capture.impact.clone(), 0xffe066, 1);
      }
      return;
    }
    if (capture.phase === "wobble") {
      const t = Math.min(1, (time - capture.start) / CAPTURE_WOBBLE_MS);
      ball.group.rotation.z = Math.sin(t * Math.PI * 6) * 0.34 * (1 - t);
      ball.group.position.set(CAPTURE_REST_X, CAPTURE_REST_Y, CAPTURE_REST_Z);
      if (t >= 1) {
        ball.group.rotation.z = 0;
        if (capture.plan.willCatch === false) {
          // 命中但没抓住：球自己弹开，宠物从球里跳回来。
          capture.phase = "release";
          capture.start = time;
          effects.sparkle(ball.group.position.clone(), 0xffffff, 2);
          notifyCaptureResolved({ caught: false, dodged: false });
          return;
        }
        caught = true;
        notifyCaptureResolved({ caught: true, dodged: false });
        capture = null;
      }
      return;
    }
    if (capture.phase === "release") {
      const t = Math.min(1, (time - capture.start) / CAPTURE_RELEASE_MS);
      ballOpen = Math.min(1, t * 2.4);
      ball.top.rotation.x = -ballOpen * 1.35;
      ball.group.scale.setScalar(1 - Math.min(0.35, t * 0.35));
      if (current) {
        current.holder.visible = true;
        const pop = Math.min(1, t * 1.5);
        current.holder.scale.setScalar(0.12 + pop * 0.88 + Math.sin(t * Math.PI) * 0.12);
      }
      if (t >= 1) {
        capture = null;
        ball.group.visible = false;
        ball.group.scale.setScalar(1);
        ball.top.rotation.x = 0;
        if (current) {
          current.holder.scale.setScalar(1);
          current.holder.visible = true;
        }
      }
    }
  }

  // setCaught 由 UI 恢复持久化状态：不播动画，直接把宠物收成球 / 放出来。
  function setCaught(value, animate) {
    const next = !!value;
    if (next) {
      if (caught && !capture) return;
      caught = true;
      capture = null;
      if (current) {
        current.holder.visible = false;
        current.holder.scale.setScalar(1);
        current.holder.position.x = 0;
      }
      ball.group.visible = true;
      ball.group.position.set(CAPTURE_REST_X, CAPTURE_REST_Y, CAPTURE_REST_Z);
      ball.group.rotation.set(0, 0, 0);
      ball.group.scale.setScalar(1);
      ball.top.rotation.x = 0;
      if (animate) effects.sparkle(ball.group.position.clone(), 0xffe066, 1);
      return;
    }
    if (!caught) return;
    caught = false;
    capture = null;
    ball.group.visible = false;
    ball.top.rotation.x = 0;
    if (current) {
      current.holder.visible = true;
      current.holder.scale.setScalar(1);
      current.holder.position.x = 0;
    }
    if (animate) effects.sparkle(new THREE.Vector3(0, 0.2, 0.5), 0xffffff, 2);
  }

  // releaseBall 播放「开球—宠物弹出」动画，返回是否真的放了出来。
  function releaseBall() {
    if (!caught) return false;
    caught = false;
    ballOpen = 0;
    capture = { phase: "release", start: performance.now() };
    effects.sparkle(ball.group.position.clone(), 0xffffff, 3);
    if (current) {
      current.holder.visible = true;
      current.holder.scale.setScalar(0.12);
    }
    return true;
  }

  function isCaught() {
    return caught;
  }

  function isCapturing() {
    return !!capture;
  }

  function applyFrame(time, delta) {
    if (!current) return;
    const nextX = smoothDamp(
      currentAimX,
      aimVelocityX,
      aimX,
      LOOK_SMOOTH_TIME_X,
      delta
    );
    const nextY = smoothDamp(
      currentAimY,
      aimVelocityY,
      aimY,
      LOOK_SMOOTH_TIME_Y,
      delta
    );
    currentAimX = nextX.value;
    aimVelocityX = nextX.velocity;
    currentAimY = nextY.value;
    aimVelocityY = nextY.velocity;
    bodyAimX = damp(bodyAimX, currentAimX, BODY_FOLLOW_LAMBDA, delta);
    const holder = current.holder;
    holder.position.y = current.baseY;
    holder.position.x = 0;
    holder.rotation.set(0, 0, 0);
    holder.scale.setScalar(1);
    if (chargeStart >= 0) {
      applyCharge(time);
      if (currentRig) applyBoneAim(currentRig, currentAimX * 0.55, currentAimY * 0.55, delta);
      if (skillStart >= 0) applySkill(time);
      applyCapture(time);
      effects.setChargeGlow(chargeGlow);
      effects.update(delta, currentAimX, currentAimY);
      return;
    }
    const breathing = reduceMotion ? 0 : Math.sin(elapsed * 1.55) * 0.008;
    const idleNod = reduceMotion ? 0 : Math.sin(elapsed * 1.9 + 0.6) * 0.004;
    const pose = gazePose(currentAimX, currentAimY);
    holder.position.y += breathing;
    // 身体只分走很小一部分角度：头颈负责「看」，身体负责「重心跟着挪」。
    holder.position.x = pose.bodyShift + bodyAimX * 0.01;
    holder.rotation.y += pose.bodyYaw + bodyAimX * 0.02;
    holder.rotation.x += pose.bodyPitch - idleNod;
    holder.rotation.z += pose.bodyRoll + currentAimX * 0.006;
    if (currentRig) applyBoneAim(currentRig, currentAimX, currentAimY, delta);
    applySkill(time);
    applyCapture(time);
    effects.setChargeGlow(chargeGlow);
    effects.update(delta, currentAimX, currentAimY);

    if (transition) {
      const p = transition.duration <= 0
        ? 1
        : Math.min(1, (time - transition.start) / transition.duration);
      const eased = 1 - Math.pow(1 - p, 3);
      setOpacity(transition.from, 1 - eased);
      setOpacity(transition.to, eased);
      transition.from.holder.scale.setScalar(1 - eased * 0.16);
      transition.to.holder.scale.multiplyScalar(0.82 + eased * 0.18);
      if (p >= 1) {
        disposeObject(transition.from.root);
        setOpacity(transition.to, 1);
        transition.to.holder.scale.setScalar(1);
        transition = null;
      }
    }
  }

  // 低配模式把渲染限到 PERFORMANCE_LOW_FPS（20fps）；软件渲染（虚拟机 / 无
  // GPU 加速）即使开着全部特效也限到 SOFTWARE_RENDER_FPS（30fps）——逐帧
  // 光栅化会占满 CPU，降帧后宠物跟随仍连贯，界面点击/切页不再被抢时间片。
  function frame(time) {
    if (!enabled || closed) {
      animationFrame = 0;
      return;
    }
    const minInterval = 1000 / (performanceLow() ? PERFORMANCE_LOW_FPS : SOFTWARE_RENDER_FPS);
    if ((performanceLow() || softwareRendering()) && time - lastDrawTime < minInterval) {
      animationFrame = requestAnimationFrame(frame);
      return;
    }
    lastDrawTime = time;
    const delta = Math.min(0.05, Math.max(0.001, (time - lastTime) / 1000));
    lastTime = time;
    elapsed += delta;
    applyFrame(time, delta);
    renderOnce();
    animationFrame = requestAnimationFrame(frame);
  }

  function startLoop() {
    if (!enabled || closed || animationFrame) return;
    lastTime = performance.now();
    animationFrame = requestAnimationFrame(frame);
  }

  function stopLoop() {
    if (!animationFrame) return;
    cancelAnimationFrame(animationFrame);
    animationFrame = 0;
  }

  function setEnabled(nextEnabled) {
    enabled = !!nextEnabled;
    if (enabled) startLoop();
    else stopLoop();
  }

  function setPointer(x, y) {
    aimX = THREE.MathUtils.clamp(Number(x) || 0, -1, 1);
    aimY = THREE.MathUtils.clamp(Number(y) || 0, -1, 1);
    if (reduceMotion) {
      currentAimX = aimX;
      currentAimY = aimY;
      aimVelocityX = 0;
      aimVelocityY = 0;
      bodyAimX = aimX;
      applyFrame(performance.now(), 1);
      renderOnce();
    }
  }

  function resetPointer() {
    aimX = 0;
    aimY = 0;
  }

  function playSkill(type, intensity, options) {
    const ultimate = !!(options && options.ultimate);
    if (chargeStart >= 0) {
      chargeStart = -1;
      clearChargeGlow();
    }
    skillType = type || "electric";
    skillPower = ultimate
      ? 5
      : Math.max(1, Math.min(5, Number(intensity) || effectIntensity));
    effectIntensity = Math.max(effectIntensity, skillPower);
    skillUltimate = ultimate;
    // 低配/软件渲染减半粒子，reduce-motion 只保留闪光与冲击环；
    // 档位差异本身由 skillProfile 保证，不在这里另开分支。
    const density = performanceLow() || softwareRendering() ? 0.55 : 1;
    skillVfx = skillProfile(skillType, skillPower, ultimate, {
      density,
      lowMotion: reduceMotion,
    });
    skillEmitted = false;
    skillStart = performance.now();
  }

  // setCharging 由长短按逻辑驱动：true 进入蓄力姿态，false 取消并复位。
  function setCharging(on) {
    const next = !!on;
    if (next === (chargeStart >= 0)) return;
    chargeStart = next ? performance.now() : -1;
    if (!next) clearChargeGlow();
  }

  function isCharging() {
    return chargeStart >= 0;
  }

  function chargeProgress() {
    if (chargeStart < 0) return 0;
    return Math.min(1, (performance.now() - chargeStart) / (CHARGE_FULL_SECONDS * 1000));
  }

  function setEffectIntensity(value) {
    effectIntensity = Math.max(1, Math.min(5, Number(value) || 1));
  }

  // 性能模式切换（设置里改 auto/on/off 或页面启动检测完成）时重设分辨率：
  // 低配降 DPR、恢复时回到原清晰度；resize 内部会补一帧。
  function handlePerformanceChange() {
    if (closed) return;
    resize();
  }

  function dispose() {
    if (closed) return;
    closed = true;
    stopLoop();
    if (resizeObserver) resizeObserver.disconnect();
    if (typeof document !== "undefined" && document.removeEventListener) {
      document.removeEventListener("qka-perf-changed", handlePerformanceChange);
    }
    if (current) disposeObject(current.root);
    if (transition) disposeObject(transition.from.root);
    effects.dispose();
    scene.traverse(function (object) {
      if (object.geometry) object.geometry.dispose();
      if (object.material) {
        if (object.material.map) object.material.map.dispose();
        object.material.dispose();
      }
    });
    draco.dispose();
    renderer.dispose();
  }

  resize();
  if (window.ResizeObserver) {
    resizeObserver = new ResizeObserver(resize);
    resizeObserver.observe(canvas);
  } else {
    window.addEventListener("resize", resize);
  }
  if (typeof document !== "undefined" && document.addEventListener) {
    document.addEventListener("qka-perf-changed", handlePerformanceChange);
  }
  canvas.addEventListener("webglcontextlost", function (event) {
    event.preventDefault();
    stopLoop();
    notifyFallback();
  }, { once: true });
  setStage(stage, false);
  startLoop();

  return {
    setStage,
    setEnabled,
    setPointer,
    resetPointer,
    playSkill,
    setCharging,
    isCharging,
    chargeProgress,
    setEffectIntensity,
    throwBall,
    setCaught,
    releaseBall,
    isCaught,
    isCapturing,
    resize,
    dispose,
    debugState: function () {
      return {
        aimX,
        aimY,
        headBone: currentRig && currentRig.head ? currentRig.head.bone.name : "",
        neckBone: currentRig && currentRig.neck ? currentRig.neck.bone.name : "",
        headQuaternion: currentRig && currentRig.head
          ? currentRig.head.bone.quaternion.toArray()
          : [],
        currentAimX,
        currentAimY,
        aimVelocityX,
        aimVelocityY,
        caught,
        capturePhase: capture ? capture.phase : "",
        effects: effects.stats(),
      };
    },
    get stage() {
      return stage;
    },
  };
}

export function createPokemon3D(canvas, options) {
  if (!canvas) throw new Error("missing pokemon canvas");
  try {
    return createStageViewer(canvas, options);
  } catch (error) {
    if (options && options.onUnavailable) options.onUnavailable(error);
    return {
      setStage: function () {},
      setEnabled: function () {},
      setPointer: function () {},
      resetPointer: function () {},
      playSkill: function () {},
      setCharging: function () {},
      isCharging: function () { return false; },
      chargeProgress: function () { return 0; },
      setEffectIntensity: function () {},
      throwBall: function () { return false; },
      setCaught: function () {},
      releaseBall: function () { return false; },
      isCaught: function () { return false; },
      isCapturing: function () { return false; },
      resize: function () {},
      dispose: function () {},
      stage: 1,
    };
  }
}
