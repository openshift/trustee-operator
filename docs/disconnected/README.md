# Disconnected Installation

This directory contains `ImageSetConfiguration` files for mirroring the
Trustee operator in disconnected (air-gapped) environments using `oc-mirror`.

## What is covered

Each `imageset-config-<ocp-version>.yaml` file mirrors:

- The Trustee operator from the Red Hat operator catalog, including the
  catalog index, operator bundle, and operator controller image.
- The KBS operand image, listed as `relatedImages` in the operator bundle
  and automatically picked up by `oc-mirror`.

One file is provided per supported OCP version.

## Usage

1. Select the file matching your OCP version, e.g. `imageset-config-4.20.yaml`.

2. Run `oc-mirror` to mirror all images to your internal registry:

   ```bash
   oc-mirror --v2 --config imageset-config-4.20.yaml \
     --workspace file://oc-mirror-workspace docker://<your-registry>
   ```

3. Apply the generated IDMS (ImageDigestMirrorSet) / ICSP (ImageContentSourcePolicy)
   and CatalogSource manifests to your cluster. These remap image references from upstream
   registries to your internal mirror:

   ```bash
   oc apply -f oc-mirror-workspace/working-dir/cluster-resources/
   ```

4. Install the operator using the mirrored catalog as the source.
