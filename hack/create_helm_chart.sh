#!/bin/bash

CONTROLLER="${2}"

check_dependency() {
  if ! command -v helm &> /dev/null
  then
      echo "Helm could not be found. Please install Helm to proceed."
      exit
  fi
    if ! command -v kustomize &> /dev/null
    then
        echo "kustomize could not be found. Please install kustomize to proceed."
        exit
    fi
}

create_helm_chart() {
  helm create "${1}/${CONTROLLER}"
  cd "${1}/${CONTROLLER}" || exit
  rm -r templates
  mkdir templates
  mkdir crds
  rm -r charts
  rm values.yaml
  cd ../helm_temp || exit

  case "${1}" in
    "helm" )
      #mac
      split -p "^---$" "install.yaml" "helm_";
      ;;
    "output" )
      #ubuntu
      csplit "install.yaml" "/^---$/" {*} --prefix "helm_" -q;
      ;;
    * )
      exit
      ;;
  esac

  rm install.yaml
  # Move into crds & templates folders after renaming
  HELM_FILES=($(ls ))
  for input in "${HELM_FILES[@]}";  do
      FILENAME=$(cat "${input}" | grep '^  name: ' | head -1 | sed 's/name: //g' | sed 's/\./_/g' | sed 's/ //g')
      TYPE=$(cat "${input}" | grep '^kind: ' | head -1 | sed 's/kind: //g' | sed 's/\./_/g' | tr '[:upper:]' '[:lower:]' | sed 's/ //g')
      if grep -q "kind: CustomResourceDefinition" "${input}" ; then
          mv ${input} ../${CONTROLLER}/crds/${FILENAME}.yaml
      else
          mv ${input} ../${CONTROLLER}/templates/${TYPE}_${FILENAME}.yaml
      fi
  done
  cd ..
  rm -rf helm_temp
}

create_from_local_resource_manifests() {
  echo "Creating from local manifests in the repository"
  rm -rf helm
  mkdir -p helm
  mkdir -p "helm/helm_temp"
  kustomize build ./config/default > ./helm/helm_temp/install.yaml

  create_helm_chart "helm"
}

create_from_github_release() {
  echo "Creating from the release"
  mkdir -p "output/helm_temp"
  cp ./output/install.yaml ./output/helm_temp/install.yaml
  create_helm_chart "output"
  tar -czf helm_chart.tar.gz ${CONTROLLER}
}

check_dependency
case "${1}" in
  "local" )
      create_from_local_resource_manifests
      ;;
  "release" )
      create_from_github_release
      ;;
  * )
    echo -n "unknown"
    exit
    ;;
esac